package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LinearPlan is a candidate plan that passed the 3-point linearity check.
type LinearPlan struct {
	IDKey      string
	RepCompany string
	Product    string
	TermValue  int
	RateType   string
	BaseFee    float64 // $ per month (decomposed)
	PerKwhRate float64 // ¢/kWh (decomposed)
	Renewable  int
	Rating     float64
	EnrollURL  string
}

type ProjectionRequest struct {
	CurrentRateCents   float64 `json:"current_rate_cents"`
	CurrentBaseFee     float64 `json:"current_base_fee"`
	ETFAmount          float64 `json:"etf_amount"`
	ContractExpiration string  `json:"contract_expiration"`
}

type ProjectionPlanInfo struct {
	IDKey              string  `json:"id_key"`
	RepCompany         string  `json:"rep_company"`
	Product            string  `json:"product"`
	TermValue          int     `json:"term_value"`
	RateType           string  `json:"rate_type"`
	ProjectedRateCents float64 `json:"projected_rate_cents"`
	ProjectedBaseFee   float64 `json:"projected_base_fee"`
	Renewable          int     `json:"renewable"`
	Rating             float64 `json:"rating"`
	EnrollURL          string  `json:"enroll_url"`
}

type SwitchEvent struct {
	EffectivePeriod string             `json:"effective_period"` // "T+N" period label
	ETFPaid         float64            `json:"etf_paid"`
	Plan            ProjectionPlanInfo `json:"plan"`
}

type PeriodBreakdown struct {
	Period           string  `json:"period"`            // "T+N" period label
	PeriodStart      string  `json:"period_start"`      // "YYYY-MM-DD"
	PeriodEnd        string  `json:"period_end"`        // "YYYY-MM-DD" (inclusive last day)
	UsageKwh         float64 `json:"usage_kwh"`
	UsageIsEstimated bool    `json:"usage_is_estimated"`
	ActivePlanLabel  string  `json:"active_plan_label"`
	RateCents        float64 `json:"rate_cents"`
	BaseFee          float64 `json:"base_fee"`
	PeriodCost       float64 `json:"period_cost"`
	Confidence       string  `json:"confidence"`
}

type StrategyResult struct {
	StrategyID             string            `json:"strategy_id"`
	StrategyName           string            `json:"strategy_name"`
	TotalCost              float64           `json:"total_cost"`
	TotalSavingsVsBaseline float64           `json:"total_savings_vs_baseline"`
	ETFPaid                float64           `json:"etf_paid"`
	NetSavings             float64           `json:"net_savings"`
	SwitchCount            int               `json:"switch_count"`
	Confidence             string            `json:"confidence"`
	Switches               []SwitchEvent     `json:"switches"`
	PeriodBreakdown        []PeriodBreakdown `json:"period_breakdown"`
}

// planSegment is a half-open interval [start, end) covered by a single plan.
// If isVar is true the plan field is ignored and the variable rate is re-projected
// to the specific period at breakdown time.
type planSegment struct {
	start  time.Time
	end    time.Time
	plan   activePlan
	isVar  bool
}

// activePlan holds the effective rates for a plan within a segment.
type activePlan struct {
	label     string
	rateCents float64
	baseFee   float64
}

// periodCoversSegment reports whether [segStart, segEnd) overlaps [periodStart, periodEnd).
// All segment boundaries in practice align with period boundaries, so this always returns
// true or false (never a partial fraction).
func periodCoversSegment(periodStart, periodEnd, segStart, segEnd time.Time) bool {
	return segStart.Before(periodEnd) && segEnd.After(periodStart)
}

// periodLabel returns the T+N label for period index i (1-based).
func periodLabel(i int) string {
	return fmt.Sprintf("T+%d", i+1)
}

// periodConfidence returns confidence based on how far periodStart is from today.
func periodConfidence(periodStart, today time.Time) string {
	monthsAhead := (periodStart.Year()-today.Year())*12 + int(periodStart.Month()) - int(today.Month())
	if monthsAhead < 2 {
		return "high"
	} else if monthsAhead < 6 {
		return "medium"
	}
	return "low"
}

func lowestConfidence(a, b string) string {
	order := map[string]int{"high": 2, "medium": 1, "low": 0}
	if order[a] < order[b] {
		return a
	}
	return b
}


type planResult struct {
	plan activePlan
	info ProjectionPlanInfo
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func computeProjection(ctx context.Context, pool *pgxpool.Pool, req ProjectionRequest, today time.Time) ([]StrategyResult, error) {
	expiry, err := time.Parse("2006-01-02", req.ContractExpiration)
	if err != nil {
		return nil, fmt.Errorf("invalid contract_expiration: %w", err)
	}
	expiry = time.Date(expiry.Year(), expiry.Month(), expiry.Day(), 0, 0, 0, 0, time.UTC)

	// T = start of the projection window:
	//   - expiry date if it's in the future
	//   - today if contract has already expired
	// The window then runs T, T+1m, T+2m, … T+12m (12 T+x periods).
	windowStart := expiry
	if expiry.Before(today) {
		windowStart = today
	}

	const numPeriods = 12

	// periodStarts[i] = T + i months; periodStarts[12] = T + 12m = windowEnd
	periodStarts := make([]time.Time, numPeriods+1)
	for i := 0; i <= numPeriods; i++ {
		periodStarts[i] = windowStart.AddDate(0, i, 0)
	}
	windowEnd := periodStarts[numPeriods]

	// Historical period starts for usage lookup (same T+i periods, 1 year back).
	histPeriodStarts := make([]time.Time, numPeriods)
	for i := 0; i < numPeriods; i++ {
		histPeriodStarts[i] = windowStart.AddDate(-1, i, 0)
	}

	linearPlans, err := queryLinearPlans(ctx, pool, today)
	if err != nil {
		return nil, fmt.Errorf("queryLinearPlans: %w", err)
	}
	usageMap, estimatedMap, err := queryPeriodUsage(ctx, pool, histPeriodStarts)
	if err != nil {
		return nil, fmt.Errorf("queryPeriodUsage: %w", err)
	}
	fixedHistoricalRates, err := queryHistoricalMinRates(ctx, pool, false)
	if err != nil {
		return nil, fmt.Errorf("queryHistoricalMinRates(fixed): %w", err)
	}
	varHistoricalRates, err := queryHistoricalMinRates(ctx, pool, true)
	if err != nil {
		return nil, fmt.Errorf("queryHistoricalMinRates(variable): %w", err)
	}

	// switchDateExpiry: when at-expiry strategies start the new plan.
	switchDateExpiry := expiry
	if switchDateExpiry.Before(windowStart) {
		switchDateExpiry = windowStart
	}

	switchDateNow := today

	// ETF applies if switching today is before (expiry − 14 days).
	etfCutoff := expiry.AddDate(0, 0, -14)
	etfOnSwitchNow := 0.0
	if today.Before(etfCutoff) {
		etfOnSwitchNow = req.ETFAmount
	}

	// Average usage fallback across periods with known history.
	totalUsage, usageCount := 0.0, 0
	for i := 0; i < numPeriods; i++ {
		if u, ok := usageMap[i]; ok && u > 0 {
			totalUsage += u
			usageCount++
		}
	}
	avgUsage := 500.0
	if usageCount > 0 {
		avgUsage = totalUsage / float64(usageCount)
	}

	usageForPeriod := func(periodIdx int) (float64, bool) {
		u, ok := usageMap[periodIdx]
		if !ok || u == 0 {
			return avgUsage, true
		}
		return u, estimatedMap[periodIdx]
	}

	// dateToPeriod maps a calendar date to the T+N label of the period it falls in.
	dateToPeriod := func(d time.Time) string {
		for i := 0; i < numPeriods; i++ {
			if !d.Before(periodStarts[i]) && d.Before(periodStarts[i+1]) {
				return periodLabel(i)
			}
		}
		return periodLabel(numPeriods)
	}

	// historicalRateCents returns the projected ¢/kWh for the given month using last
	// year's best rate for that calendar month. Falls back to the plan's current rate.
	// historicalMinRates values are in $/kWh (kwh1000 column); multiply by 100 for ¢/kWh.
	historicalRateCents := func(decisionDate time.Time, historicalRates map[string]float64, fallbackCents float64) float64 {
		key := time.Date(decisionDate.Year()-1, decisionDate.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
		if rate, ok := historicalRates[key]; ok {
			return rate * 100
		}
		return fallbackCents
	}

	// getFixed selects the best fixed plan for the given term and decision date.
	// Selection uses today's plan rates and actual per-period usage for each period
	// covered by the term. The projected rate shown is last year's same-month rate.
	getFixed := func(term int, decisionDate time.Time) *planResult {
		termEnd := decisionDate.AddDate(0, term, 0)
		var bestPlan *LinearPlan
		bestTotalCost := math.MaxFloat64
		for i := range linearPlans {
			plan := &linearPlans[i]
			if plan.RateType == "Variable" || plan.TermValue != term {
				continue
			}
			totalCost := 0.0
			for j := 0; j < numPeriods; j++ {
				if !periodCoversSegment(periodStarts[j], periodStarts[j+1], decisionDate, termEnd) {
					continue
				}
				usageKwh, _ := usageForPeriod(j)
				totalCost += plan.BaseFee + usageKwh*plan.PerKwhRate/100.0
			}
			if totalCost < bestTotalCost {
				bestTotalCost = totalCost
				bestPlan = plan
			}
		}
		if bestPlan == nil {
			return nil
		}
		// Historical composite rate: baseFee is baked in, so set baseFee=0.
		projRateCents := historicalRateCents(decisionDate, fixedHistoricalRates, bestPlan.PerKwhRate)
		return &planResult{
			plan: activePlan{
				label:     fmt.Sprintf("%s – %s (%dm Fixed)", bestPlan.RepCompany, bestPlan.Product, term),
				rateCents: projRateCents,
				baseFee:   0,
			},
			info: ProjectionPlanInfo{
				IDKey: bestPlan.IDKey, RepCompany: bestPlan.RepCompany, Product: bestPlan.Product,
				TermValue: bestPlan.TermValue, RateType: bestPlan.RateType,
				ProjectedRateCents: projRateCents, ProjectedBaseFee: 0,
				Renewable: bestPlan.Renewable, Rating: bestPlan.Rating, EnrollURL: bestPlan.EnrollURL,
			},
		}
	}

	// getVar selects the best variable plan for the period containing decisionDate.
	// Selection uses today's plan rates and actual usage for that period.
	// The projected rate shown is last year's same-month rate.
	getVar := func(decisionDate time.Time) *planResult {
		// Find the period index for this decision date (always a period boundary).
		periodUsage := avgUsage
		for j := 0; j < numPeriods; j++ {
			if periodStarts[j].Equal(decisionDate) {
				periodUsage, _ = usageForPeriod(j)
				break
			}
		}
		var bestPlan *LinearPlan
		bestCost := math.MaxFloat64
		for i := range linearPlans {
			plan := &linearPlans[i]
			if plan.RateType != "Variable" {
				continue
			}
			cost := plan.BaseFee + periodUsage*plan.PerKwhRate/100.0
			if cost < bestCost {
				bestCost = cost
				bestPlan = plan
			}
		}
		if bestPlan == nil {
			return nil
		}
		projRateCents := historicalRateCents(decisionDate, varHistoricalRates, bestPlan.PerKwhRate)
		return &planResult{
			plan: activePlan{
				label:     fmt.Sprintf("%s – %s (Variable)", bestPlan.RepCompany, bestPlan.Product),
				rateCents: projRateCents,
				baseFee:   0,
			},
			info: ProjectionPlanInfo{
				IDKey: bestPlan.IDKey, RepCompany: bestPlan.RepCompany, Product: bestPlan.Product,
				TermValue: bestPlan.TermValue, RateType: "Variable",
				ProjectedRateCents: projRateCents, ProjectedBaseFee: 0,
				Renewable: bestPlan.Renewable, Rating: bestPlan.Rating, EnrollURL: bestPlan.EnrollURL,
			},
		}
	}

	currentActivePlan := activePlan{
		label:     "Current plan",
		rateCents: req.CurrentRateCents,
		baseFee:   req.CurrentBaseFee,
	}

	// ── buildBreakdown ────────────────────────────────────────────────────────
	// For each T+x period, find which segment covers it and compute cost.
	// All segment boundaries align with period boundaries so each period is
	// covered by exactly one segment. Variable segments re-project to the
	// period's start date.
	buildBreakdown := func(segments []planSegment) ([]PeriodBreakdown, float64) {
		breakdown := make([]PeriodBreakdown, numPeriods)
		total := 0.0
		for i := 0; i < numPeriods; i++ {
			periodStart := periodStarts[i]
			periodEnd := periodStarts[i+1]
			usageKwh, isEstimated := usageForPeriod(i)

			var segPlan activePlan
			for _, seg := range segments {
				if !periodCoversSegment(periodStart, periodEnd, seg.start, seg.end) {
					continue
				}
				segPlan = seg.plan
				if seg.isVar {
					if varResult := getVar(periodStart); varResult != nil {
						segPlan = varResult.plan
					}
				}
				break // each period is covered by exactly one segment
			}

			cost := segPlan.baseFee + usageKwh*segPlan.rateCents/100.0
			total += cost
			// period_end is shown as the last day (inclusive) = periodEnd - 1 day
			lastDay := periodEnd.AddDate(0, 0, -1)
			breakdown[i] = PeriodBreakdown{
				Period:           periodLabel(i),
				PeriodStart:      periodStart.Format("2006-01-02"),
				PeriodEnd:        lastDay.Format("2006-01-02"),
				UsageKwh:         round2(usageKwh),
				UsageIsEstimated: isEstimated,
				ActivePlanLabel:  segPlan.label,
				RateCents:        round2(segPlan.rateCents),
				BaseFee:          round2(segPlan.baseFee),
				PeriodCost:       round2(cost),
				Confidence:       periodConfidence(periodStart, today),
			}
		}
		return breakdown, total
	}

	buildResult := func(id, name string, segments []planSegment, switches []SwitchEvent, etfPaid float64) StrategyResult {
		breakdown, total := buildBreakdown(segments)
		confidence := "high"
		for _, switchEvt := range switches {
			for i := 0; i < numPeriods; i++ {
				if periodLabel(i) == switchEvt.EffectivePeriod {
					confidence = lowestConfidence(confidence, periodConfidence(periodStarts[i], today))
				}
			}
		}
		if switches == nil {
			switches = []SwitchEvent{}
		}
		return StrategyResult{
			StrategyID:      id,
			StrategyName:    name,
			TotalCost:       round2(total),
			ETFPaid:         etfPaid,
			SwitchCount:     len(switches),
			Confidence:      confidence,
			Switches:        switches,
			PeriodBreakdown: breakdown,
		}
	}

	// ── buildFixedRolling ─────────────────────────────────────────────────────
	// Builds date-based plan segments for a rolling fixed-term strategy.
	// switchDate: the date the first new plan starts.
	// termMonths: plan term in calendar months (3, 6, or 12).
	// initialETF: ETF amount charged on the first switch (0 if none).
	buildFixedRolling := func(switchDate time.Time, termMonths int, initialETF float64) ([]planSegment, []SwitchEvent) {
		var segments []planSegment
		var switches []SwitchEvent

		// Pre-switch: current plan from window start to the switch date.
		if switchDate.After(windowStart) {
			end := switchDate
			if end.After(windowEnd) {
				end = windowEnd
			}
			segments = append(segments, planSegment{start: windowStart, end: end, plan: currentActivePlan})
		}

		decisionDate := switchDate
		if decisionDate.Before(windowStart) {
			decisionDate = windowStart
		}
		first := true
		for decisionDate.Before(windowEnd) {
			planRes := getFixed(termMonths, decisionDate)
			if planRes == nil {
				planRes = getVar(decisionDate)
			}
			if planRes == nil {
				break
			}
			nextDate := decisionDate.AddDate(0, termMonths, 0)
			segEnd := nextDate
			if segEnd.After(windowEnd) {
				segEnd = windowEnd
			}
			etf := 0.0
			if first {
				etf = initialETF
				first = false
			}
			segments = append(segments, planSegment{start: decisionDate, end: segEnd, plan: planRes.plan})
			switches = append(switches, SwitchEvent{
				EffectivePeriod: dateToPeriod(decisionDate),
				ETFPaid:         etf,
				Plan:            planRes.info,
			})
			decisionDate = nextDate
		}
		return segments, switches
	}

	// costForDateRange sums the projected cost for the given activePlan over all
	// periods that fall within [startDate, endDate). Returns (totalCost, periodsCovered).
	costForDateRange := func(plan activePlan, startDate, endDate time.Time) (float64, int) {
		totalCost := 0.0
		periodsCovered := 0
		for i := 0; i < numPeriods; i++ {
			periodStart := periodStarts[i]
			periodEnd := periodStarts[i+1]
			if !periodCoversSegment(periodStart, periodEnd, startDate, endDate) {
				continue
			}
			usageKwh, _ := usageForPeriod(i)
			totalCost += plan.baseFee + usageKwh*plan.rateCents/100.0
			periodsCovered++
		}
		return totalCost, periodsCovered
	}

	var results []StrategyResult

	// ── 1. BASELINE ───────────────────────────────────────────────────────────
	// Stay on current plan until expiry; roll to variable (re-projected per period).
	{
		segments := []planSegment{}
		if switchDateExpiry.After(windowStart) {
			segments = append(segments, planSegment{start: windowStart, end: switchDateExpiry, plan: currentActivePlan})
		}
		if switchDateExpiry.Before(windowEnd) {
			segments = append(segments, planSegment{start: switchDateExpiry, end: windowEnd, isVar: true})
		}
		results = append(results, buildResult("baseline", "Baseline — stay on current, roll to variable at expiry", segments, nil, 0))
	}

	// ── 2. SWITCH_AT_EXPIRY_12M ───────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(switchDateExpiry, 12, 0)
		results = append(results, buildResult("switch_at_expiry_12m", "Switch at expiry — 12-month fixed", segments, switches, 0))
	}

	// ── 3. SWITCH_AT_EXPIRY_6M ────────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(switchDateExpiry, 6, 0)
		results = append(results, buildResult("switch_at_expiry_6m", "Switch at expiry — 6-month rolling", segments, switches, 0))
	}

	// ── 4. SWITCH_AT_EXPIRY_3M ────────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(switchDateExpiry, 3, 0)
		results = append(results, buildResult("switch_at_expiry_3m", "Switch at expiry — 3-month rolling", segments, switches, 0))
	}

	// ── 5. SWITCH_NOW_12M ─────────────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(switchDateNow, 12, etfOnSwitchNow)
		results = append(results, buildResult("switch_now_12m", "Switch now — 12-month fixed", segments, switches, etfOnSwitchNow))
	}

	// ── 6. SWITCH_NOW_3M ──────────────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(switchDateNow, 3, etfOnSwitchNow)
		results = append(results, buildResult("switch_now_3m", "Switch now — 3-month rolling", segments, switches, etfOnSwitchNow))
	}

	// ── 7. OPTIMAL_GREEDY ─────────────────────────────────────────────────────
	// At each decision point (starting from expiry), pick the term that minimises
	// projected cost-per-period for the remaining window.
	{
		var segments []planSegment
		var switches []SwitchEvent

		if switchDateExpiry.After(windowStart) {
			segments = append(segments, planSegment{start: windowStart, end: switchDateExpiry, plan: currentActivePlan})
		}

		type termOption struct {
			termMonths int
			isVar      bool
		}
		termOptions := []termOption{
			{1, true},
			{3, false},
			{6, false},
			{12, false},
		}

		decisionDate := switchDateExpiry
		if decisionDate.Before(windowStart) {
			decisionDate = windowStart
		}
		for decisionDate.Before(windowEnd) {
			bestCostPerPeriod := math.MaxFloat64
			var bestPlanRes *planResult
			bestTermMonths := 1

			for _, termOpt := range termOptions {
				var planRes *planResult
				if termOpt.isVar {
					planRes = getVar(decisionDate)
				} else {
					planRes = getFixed(termOpt.termMonths, decisionDate)
				}
				if planRes == nil {
					continue
				}
				endDate := decisionDate.AddDate(0, termOpt.termMonths, 0)
				totalCost, periodsCovered := costForDateRange(planRes.plan, decisionDate, endDate)
				if periodsCovered <= 0 {
					continue
				}
				costPerPeriod := totalCost / float64(periodsCovered)
				if costPerPeriod < bestCostPerPeriod {
					bestCostPerPeriod = costPerPeriod
					bestPlanRes = planRes
					bestTermMonths = termOpt.termMonths
				}
			}

			if bestPlanRes == nil {
				break
			}
			nextDate := decisionDate.AddDate(0, bestTermMonths, 0)
			segEnd := nextDate
			if segEnd.After(windowEnd) {
				segEnd = windowEnd
			}
			segments = append(segments, planSegment{start: decisionDate, end: segEnd, plan: bestPlanRes.plan})
			switches = append(switches, SwitchEvent{
				EffectivePeriod: dateToPeriod(decisionDate),
				ETFPaid:         0,
				Plan:            bestPlanRes.info,
			})
			decisionDate = nextDate
		}
		results = append(results, buildResult("optimal_greedy", "Optimal — greedy at each decision point", segments, switches, 0))
	}

	// Savings vs baseline
	baselineCost := results[0].TotalCost
	for i := range results {
		savings := round2(baselineCost - results[i].TotalCost)
		results[i].TotalSavingsVsBaseline = savings
		results[i].NetSavings = round2(savings - results[i].ETFPaid)
	}

	return results, nil
}
