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
	IsProjected      bool    `json:"is_projected"` // true when rates are a historical estimate, not today's live rates
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
type planSegment struct {
	start time.Time
	end   time.Time
	plan  ratePlan
}

// ratePlan holds the effective rates for a plan within a segment.
type ratePlan struct {
	label     string
	rateCents float64
	baseFee   float64
	isActual  bool // true when rates come from today's live plans, not a historical projection
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
	plan ratePlan
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

	// Only fetch today's plans when today is within the 30-day enrollment window
	// of the first decision date (windowStart). Further out they are never needed.
	var todayPlans []LinearPlan
	if !today.Before(windowStart.AddDate(0, 0, -30)) {
		todayPlans, err = queryTodayPlans(ctx, pool, today)
		if err != nil {
			return nil, fmt.Errorf("queryTodayPlans: %w", err)
		}
	}
	usageMap, estimatedMap, err := queryPeriodUsage(ctx, pool, histPeriodStarts)
	if err != nil {
		return nil, fmt.Errorf("queryPeriodUsage: %w", err)
	}
	historicalPlans, err := queryHistoricalDecomposedPlans(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("queryHistoricalDecomposedPlans: %w", err)
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

	// bestHistoricalPlan finds the cheapest historical plan for decisionDate.
	//
	// If today >= decisionDate−30d (within enrollment window):
	//   - Searches [today+1d−1yr, decisionDate−1yr] to cover the gap between
	//     today and decisionDate (rates already captured by todayPlans are excluded).
	//
	// If today < decisionDate−30d (beyond enrollment window):
	//   - Searches [decisionDate−1yr−30d, decisionDate−1yr].
	//
	// Returns an error if no historical data exists for the window.
	bestHistoricalPlan := func(
		decisionDate time.Time,
		numCoveredPeriods int, totalUsage float64,
	) (baseFee, rateCents float64, err error) {
		inEnrollmentWindow := !today.Before(decisionDate.AddDate(0, 0, -30))

		var histStart, histEnd time.Time
		if inEnrollmentWindow {
			histStart = today.AddDate(-1, 0, 1)
			histEnd = decisionDate.AddDate(-1, 0, 0)
		} else {
			histStart = decisionDate.AddDate(-1, 0, -30)
			histEnd = decisionDate.AddDate(-1, 0, 0)
		}
		bestCost := math.MaxFloat64
		for dateStr, candidates := range historicalPlans {
			fetchDate, parseErr := time.Parse("2006-01-02", dateStr)
			if parseErr != nil || fetchDate.Before(histStart) || fetchDate.After(histEnd) {
				continue
			}
			for _, candidate := range candidates {
				cost := float64(numCoveredPeriods)*candidate.BaseFee + totalUsage*candidate.PerKwhRate/100.0
				if cost < bestCost {
					bestCost = cost
					baseFee = candidate.BaseFee
					rateCents = candidate.PerKwhRate
				}
			}
		}
		if bestCost == math.MaxFloat64 {
			return 0, 0, fmt.Errorf("no historical rate data for decision date %s", decisionDate.Format("2006-01-02"))
		}
		return baseFee, rateCents, nil
	}

	// makeHistoricalResult builds a projected planResult from historical rates.
	// Used both when historical beats live rates and when no live plans exist.
	makeHistoricalResult := func(termMonths int, fee, rate float64) *planResult {
		rateType := "Fixed"
		var label string
		if termMonths == 1 {
			rateType = "Variable"
			label = "Best variable plan (projected)"
		} else {
			label = fmt.Sprintf("Best %dm fixed plan (projected)", termMonths)
		}
		return &planResult{
			plan: ratePlan{label: label, rateCents: rate, baseFee: fee, isActual: false},
			info: ProjectionPlanInfo{
				TermValue: termMonths, RateType: rateType,
				ProjectedRateCents: rate, ProjectedBaseFee: fee,
			},
		}
	}

	// selectBestPlan finds the cheapest plan for decisionDate with the given term.
	// termMonths == 1 selects variable plans; termMonths > 1 selects fixed plans.
	//
	// Within the 30-day enrollment window (today >= decisionDate−30d), today's live
	// plans are considered first. Historical plans are always checked and override if
	// cheaper. Returns nil if no data exists from either source.
	selectBestPlan := func(termMonths int, decisionDate time.Time) *planResult {
		termEnd := decisionDate.AddDate(0, termMonths, 0)

		termUsage, numTermPeriods := 0.0, 0
		for j := 0; j < numPeriods; j++ {
			if !periodCoversSegment(periodStarts[j], periodStarts[j+1], decisionDate, termEnd) {
				continue
			}
			usageKwh, _ := usageForPeriod(j)
			termUsage += usageKwh
			numTermPeriods++
		}
		if numTermPeriods == 0 {
			numTermPeriods = termMonths
		}

		// Phase 1: find the best live plan (only within the enrollment window).
		var best *LinearPlan
		bestCost := math.MaxFloat64
		isActual := false
		if !today.Before(decisionDate.AddDate(0, 0, -30)) {
			for i := range todayPlans {
				plan := &todayPlans[i]
				if plan.TermValue != termMonths {
					continue
				}
				cost := float64(numTermPeriods)*plan.BaseFee + termUsage*plan.PerKwhRate/100.0
				if cost < bestCost {
					bestCost = cost
					best = plan
					isActual = true
				}
			}
		}

		// Phase 2: check historical plans — always runs, overrides if cheaper.
		if histFee, histRate, histErr := bestHistoricalPlan(decisionDate, numTermPeriods, termUsage); histErr == nil {
			if histCost := float64(numTermPeriods)*histFee + termUsage*histRate/100.0; histCost < bestCost {
				return makeHistoricalResult(termMonths, histFee, histRate)
			}
		}

		if best == nil {
			return nil
		}

		// Phase 3: construct result from the winning live plan.
		var label string
		if termMonths == 1 {
			label = fmt.Sprintf("%s – %s (Variable)", best.RepCompany, best.Product)
		} else {
			label = fmt.Sprintf("%s – %s (%dm Fixed)", best.RepCompany, best.Product, termMonths)
		}
		return &planResult{
			plan: ratePlan{label: label, rateCents: best.PerKwhRate, baseFee: best.BaseFee, isActual: isActual},
			info: ProjectionPlanInfo{
				IDKey: best.IDKey, RepCompany: best.RepCompany, Product: best.Product,
				TermValue: best.TermValue, RateType: best.RateType,
				ProjectedRateCents: best.PerKwhRate, ProjectedBaseFee: best.BaseFee,
				Renewable: best.Renewable, Rating: best.Rating, EnrollURL: best.EnrollURL,
			},
		}
	}

	currentActivePlan := ratePlan{
		label:     "Current plan",
		rateCents: req.CurrentRateCents,
		baseFee:   req.CurrentBaseFee,
		isActual:  true, // user-provided rates are real, not projected
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

			var segPlan ratePlan
			for _, seg := range segments {
				if !periodCoversSegment(periodStart, periodEnd, seg.start, seg.end) {
					continue
				}
				segPlan = seg.plan
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
				IsProjected:      !segPlan.isActual,
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
			planRes := selectBestPlan(termMonths, decisionDate)
			if planRes == nil {
				planRes = selectBestPlan(1, decisionDate)
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

	// costForDateRange sums the projected cost for the given ratePlan over all
	// periods that fall within [startDate, endDate). Returns (totalCost, periodsCovered).
	costForDateRange := func(plan ratePlan, startDate, endDate time.Time) (float64, int) {
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
	// Roll to variable at windowStart (= max(expiry, today)).
	{
		varRes := selectBestPlan(1, windowStart)
		var segments []planSegment
		if varRes != nil {
			segments = []planSegment{{start: windowStart, end: windowEnd, plan: varRes.plan}}
		}
		results = append(results, buildResult("baseline", "Baseline — stay on current, roll to variable at expiry", segments, nil, 0))
	}

	// ── 2. SWITCH_AT_EXPIRY_12M ───────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(windowStart, 12, 0)
		results = append(results, buildResult("switch_at_expiry_12m", "Switch at expiry — 12-month fixed", segments, switches, 0))
	}

	// ── 3. SWITCH_AT_EXPIRY_6M ────────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(windowStart, 6, 0)
		results = append(results, buildResult("switch_at_expiry_6m", "Switch at expiry — 6-month rolling", segments, switches, 0))
	}

	// ── 4. SWITCH_AT_EXPIRY_3M ────────────────────────────────────────────────
	{
		segments, switches := buildFixedRolling(windowStart, 3, 0)
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
	// At each decision point (starting from windowStart), pick the term that
	// minimises projected cost-per-period for the remaining window.
	{
		var segments []planSegment
		var switches []SwitchEvent

		termOptions := []int{1, 3, 6, 12}

		decisionDate := windowStart
		for decisionDate.Before(windowEnd) {
			bestCostPerPeriod := math.MaxFloat64
			var bestPlanRes *planResult
			bestTermMonths := 1

			for _, termMonths := range termOptions {
				planRes := selectBestPlan(termMonths, decisionDate)
				if planRes == nil {
					continue
				}
				// Evaluate each option over the full remaining window so that
				// all terms are compared on the same horizon. Using only the
				// term's own duration biases the comparison toward shorter
				// terms: a 1-month variable plan only needs to beat a
				// 12-month average to win, letting it dominate every period.
				totalCost, periodsCovered := costForDateRange(planRes.plan, decisionDate, windowEnd)
				if periodsCovered <= 0 {
					continue
				}
				costPerPeriod := totalCost / float64(periodsCovered)
				if costPerPeriod < bestCostPerPeriod {
					bestCostPerPeriod = costPerPeriod
					bestPlanRes = planRes
					bestTermMonths = termMonths
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
