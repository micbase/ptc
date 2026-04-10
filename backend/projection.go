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
	StrategyID             string             `json:"strategy_id"`
	StrategyName           string             `json:"strategy_name"`
	TotalCost              float64            `json:"total_cost"`
	TotalSavingsVsBaseline float64            `json:"total_savings_vs_baseline"`
	ETFPaid                float64            `json:"etf_paid"`
	NetSavings             float64            `json:"net_savings"`
	SwitchCount            int                `json:"switch_count"`
	Confidence             string             `json:"confidence"`
	Switches               []SwitchEvent      `json:"switches"`
	PeriodBreakdown        []PeriodBreakdown  `json:"period_breakdown"`
}

// planSegment is a half-open interval [start, end) covered by a single plan.
// If isVar is true the ap field is ignored and the variable rate is re-projected
// to the specific period at breakdown time.
type planSegment struct {
	start time.Time
	end   time.Time
	ap    activePlan
	isVar bool
}

// activePlan holds the effective rates for a plan within a segment.
type activePlan struct {
	label     string
	rateCents float64
	baseFee   float64
}

// overlapFrac returns the fraction of [periodStart, periodEnd) covered by segment [segStart, segEnd).
func overlapFrac(periodStart, periodEnd, segStart, segEnd time.Time) float64 {
	lo, hi := segStart, segEnd
	if lo.Before(periodStart) {
		lo = periodStart
	}
	if hi.After(periodEnd) {
		hi = periodEnd
	}
	if !hi.After(lo) {
		return 0
	}
	periodDays := periodEnd.Sub(periodStart).Hours() / 24
	return hi.Sub(lo).Hours() / 24 / periodDays
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

// seasonalRatio computes historical_min_rate(decisionMonth-1yr) / historical_min_rate(today-1yr).
func seasonalRatio(decisionPoint, today time.Time, rates map[string]float64) float64 {
	sameLY := time.Date(decisionPoint.Year()-1, decisionPoint.Month(), 1, 0, 0, 0, 0, time.UTC)
	todayLY := time.Date(today.Year()-1, today.Month(), 1, 0, 0, 0, 0, time.UTC)
	num, ok1 := rates[sameLY.Format("2006-01")]
	den, ok2 := rates[todayLY.Format("2006-01")]
	if !ok1 || !ok2 || den == 0 {
		return 1.0
	}
	return num / den
}

// planMonthlyCost returns the estimated monthly cost for a plan at the given usage.
func planMonthlyCost(p *LinearPlan, usageKwh float64) float64 {
	return p.BaseFee + usageKwh*p.PerKwhRate/100.0
}

func bestFixedPlanForTerm(plans []LinearPlan, term int, usageKwh float64) *LinearPlan {
	var best *LinearPlan
	for i := range plans {
		p := &plans[i]
		if p.RateType == "Variable" || p.TermValue != term {
			continue
		}
		if best == nil || planMonthlyCost(p, usageKwh) < planMonthlyCost(best, usageKwh) {
			best = p
		}
	}
	return best
}

func bestVariablePlan(plans []LinearPlan, usageKwh float64) *LinearPlan {
	var best *LinearPlan
	for i := range plans {
		p := &plans[i]
		if p.RateType != "Variable" {
			continue
		}
		if best == nil || planMonthlyCost(p, usageKwh) < planMonthlyCost(best, usageKwh) {
			best = p
		}
	}
	return best
}

type planResult struct {
	ap   activePlan
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
	fixedRates, err := queryHistoricalMinRates(ctx, pool, false)
	if err != nil {
		return nil, fmt.Errorf("queryHistoricalMinRates(fixed): %w", err)
	}
	varRates, err := queryHistoricalMinRates(ctx, pool, true)
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

	usage := func(idx int) (float64, bool) {
		u, ok := usageMap[idx]
		if !ok || u == 0 {
			return avgUsage, true
		}
		return u, estimatedMap[idx]
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

	projectFixed := func(p *LinearPlan, dp time.Time) (rateCents, baseFee float64) {
		r := seasonalRatio(dp, today, fixedRates)
		return p.PerKwhRate * r, p.BaseFee * r
	}
	projectVar := func(p *LinearPlan, dp time.Time) (rateCents, baseFee float64) {
		r := seasonalRatio(dp, today, varRates)
		return p.PerKwhRate * r, p.BaseFee * r
	}

	getFixed := func(term int, dp time.Time) *planResult {
		p := bestFixedPlanForTerm(linearPlans, term, avgUsage)
		if p == nil {
			return nil
		}
		rc, bf := projectFixed(p, dp)
		return &planResult{
			ap: activePlan{
				label:     fmt.Sprintf("%s – %s (%dm Fixed)", p.RepCompany, p.Product, term),
				rateCents: rc,
				baseFee:   bf,
			},
			info: ProjectionPlanInfo{
				IDKey: p.IDKey, RepCompany: p.RepCompany, Product: p.Product,
				TermValue: p.TermValue, RateType: p.RateType,
				ProjectedRateCents: rc, ProjectedBaseFee: bf,
				Renewable: p.Renewable, Rating: p.Rating, EnrollURL: p.EnrollURL,
			},
		}
	}

	getVar := func(dp time.Time) *planResult {
		p := bestVariablePlan(linearPlans, avgUsage)
		if p == nil {
			return nil
		}
		rc, bf := projectVar(p, dp)
		return &planResult{
			ap: activePlan{
				label:     fmt.Sprintf("%s – %s (Variable)", p.RepCompany, p.Product),
				rateCents: rc,
				baseFee:   bf,
			},
			info: ProjectionPlanInfo{
				IDKey: p.IDKey, RepCompany: p.RepCompany, Product: p.Product,
				TermValue: p.TermValue, RateType: "Variable",
				ProjectedRateCents: rc, ProjectedBaseFee: bf,
				Renewable: p.Renewable, Rating: p.Rating, EnrollURL: p.EnrollURL,
			},
		}
	}

	currentAP := activePlan{
		label:     "Current plan",
		rateCents: req.CurrentRateCents,
		baseFee:   req.CurrentBaseFee,
	}

	// ── buildBreakdown ────────────────────────────────────────────────────────
	// For each T+x period, sum contributions from all overlapping segments
	// pro-rated by the fraction of days they cover within the period.
	// Variable segments re-project to the period's start date.
	buildBreakdown := func(segs []planSegment) ([]PeriodBreakdown, float64) {
		bd := make([]PeriodBreakdown, numPeriods)
		total := 0.0
		for i := 0; i < numPeriods; i++ {
			pStart := periodStarts[i]
			pEnd := periodStarts[i+1]
			u, isEst := usage(i)
			cost, blendRate, blendBase := 0.0, 0.0, 0.0
			label := ""
			for _, seg := range segs {
				frac := overlapFrac(pStart, pEnd, seg.start, seg.end)
				if frac <= 0 {
					continue
				}
				ap := seg.ap
				if seg.isVar {
					if vp := getVar(pStart); vp != nil {
						ap = vp.ap
					}
				}
				cost += frac * (ap.baseFee + u*ap.rateCents/100.0)
				blendRate += frac * ap.rateCents
				blendBase += frac * ap.baseFee
				if label == "" {
					label = ap.label
				} else {
					label = label + " / " + ap.label
				}
			}
			total += cost
			// period_end is shown as the last day (inclusive) = pEnd - 1 day
			lastDay := pEnd.AddDate(0, 0, -1)
			bd[i] = PeriodBreakdown{
				Period:           periodLabel(i),
				PeriodStart:      pStart.Format("2006-01-02"),
				PeriodEnd:        lastDay.Format("2006-01-02"),
				UsageKwh:         round2(u),
				UsageIsEstimated: isEst,
				ActivePlanLabel:  label,
				RateCents:        round2(blendRate),
				BaseFee:          round2(blendBase),
				PeriodCost:       round2(cost),
				Confidence:       periodConfidence(pStart, today),
			}
		}
		return bd, total
	}

	buildResult := func(id, name string, segs []planSegment, switches []SwitchEvent, etfPaid float64) StrategyResult {
		bd, total := buildBreakdown(segs)
		conf := "high"
		for _, sw := range switches {
			for i := 0; i < numPeriods; i++ {
				if periodLabel(i) == sw.EffectivePeriod {
					conf = lowestConfidence(conf, periodConfidence(periodStarts[i], today))
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
			Confidence:      conf,
			Switches:        switches,
			PeriodBreakdown: bd,
		}
	}

	// ── buildFixedRolling ─────────────────────────────────────────────────────
	// Builds date-based plan segments for a rolling fixed-term strategy.
	// switchDate: the date the first new plan starts (may be mid-period).
	// termMonths: plan term in calendar months (3, 6, or 12).
	// etf0: ETF amount charged on the first switch (0 if none).
	buildFixedRolling := func(switchDate time.Time, termMonths int, etf0 float64) ([]planSegment, []SwitchEvent) {
		var segs []planSegment
		var switches []SwitchEvent

		// Pre-switch: current plan from window start to the switch date.
		if switchDate.After(windowStart) {
			end := switchDate
			if end.After(windowEnd) {
				end = windowEnd
			}
			segs = append(segs, planSegment{start: windowStart, end: end, ap: currentAP})
		}

		dpDate := switchDate
		if dpDate.Before(windowStart) {
			dpDate = windowStart
		}
		first := true
		for dpDate.Before(windowEnd) {
			pr := getFixed(termMonths, dpDate)
			if pr == nil {
				pr = getVar(dpDate)
			}
			if pr == nil {
				break
			}
			nextDate := dpDate.AddDate(0, termMonths, 0)
			segEnd := nextDate
			if segEnd.After(windowEnd) {
				segEnd = windowEnd
			}
			etf := 0.0
			if first {
				etf = etf0
				first = false
			}
			segs = append(segs, planSegment{start: dpDate, end: segEnd, ap: pr.ap})
			switches = append(switches, SwitchEvent{
				EffectivePeriod: dateToPeriod(dpDate),
				ETFPaid:        etf,
				Plan:           pr.info,
			})
			dpDate = nextDate
		}
		return segs, switches
	}

	// costForDateRange sums the projected cost for ap over [startDate, endDate)
	// intersected with our window. Returns (total_cost, periods_covered).
	costForDateRange := func(ap activePlan, startDate, endDate time.Time) (float64, float64) {
		total, covered := 0.0, 0.0
		for i := 0; i < numPeriods; i++ {
			pStart := periodStarts[i]
			pEnd := periodStarts[i+1]
			frac := overlapFrac(pStart, pEnd, startDate, endDate)
			if frac <= 0 {
				continue
			}
			u, _ := usage(i)
			total += frac * (ap.baseFee + u*ap.rateCents/100.0)
			covered += frac
		}
		return total, covered
	}

	var results []StrategyResult

	// ── 1. BASELINE ───────────────────────────────────────────────────────────
	// Stay on current plan until expiry; roll to variable (re-projected per period).
	{
		segs := []planSegment{}
		if switchDateExpiry.After(windowStart) {
			segs = append(segs, planSegment{start: windowStart, end: switchDateExpiry, ap: currentAP})
		}
		if switchDateExpiry.Before(windowEnd) {
			segs = append(segs, planSegment{start: switchDateExpiry, end: windowEnd, isVar: true})
		}
		results = append(results, buildResult("baseline", "Baseline — stay on current, roll to variable at expiry", segs, nil, 0))
	}

	// ── 2. SWITCH_AT_EXPIRY_12M ───────────────────────────────────────────────
	{
		segs, sws := buildFixedRolling(switchDateExpiry, 12, 0)
		results = append(results, buildResult("switch_at_expiry_12m", "Switch at expiry — 12-month fixed", segs, sws, 0))
	}

	// ── 3. SWITCH_AT_EXPIRY_6M ────────────────────────────────────────────────
	{
		segs, sws := buildFixedRolling(switchDateExpiry, 6, 0)
		results = append(results, buildResult("switch_at_expiry_6m", "Switch at expiry — 6-month rolling", segs, sws, 0))
	}

	// ── 4. SWITCH_AT_EXPIRY_3M ────────────────────────────────────────────────
	{
		segs, sws := buildFixedRolling(switchDateExpiry, 3, 0)
		results = append(results, buildResult("switch_at_expiry_3m", "Switch at expiry — 3-month rolling", segs, sws, 0))
	}

	// ── 5. SWITCH_NOW_12M ─────────────────────────────────────────────────────
	{
		segs, sws := buildFixedRolling(switchDateNow, 12, etfOnSwitchNow)
		results = append(results, buildResult("switch_now_12m", "Switch now — 12-month fixed", segs, sws, etfOnSwitchNow))
	}

	// ── 6. SWITCH_NOW_3M ──────────────────────────────────────────────────────
	{
		segs, sws := buildFixedRolling(switchDateNow, 3, etfOnSwitchNow)
		results = append(results, buildResult("switch_now_3m", "Switch now — 3-month rolling", segs, sws, etfOnSwitchNow))
	}

	// ── 7. OPTIMAL_GREEDY ─────────────────────────────────────────────────────
	// At each decision point (starting from expiry), pick the term that minimises
	// projected cost-per-period for the remaining window.
	{
		var segs []planSegment
		var switches []SwitchEvent

		if switchDateExpiry.After(windowStart) {
			segs = append(segs, planSegment{start: windowStart, end: switchDateExpiry, ap: currentAP})
		}

		type termOption struct {
			termMonths int
			isVar      bool
		}
		options := []termOption{
			{1, true},
			{3, false},
			{6, false},
			{12, false},
		}

		dpDate := switchDateExpiry
		if dpDate.Before(windowStart) {
			dpDate = windowStart
		}
		for dpDate.Before(windowEnd) {
			bestCostPerPeriod := math.MaxFloat64
			var bestPR *planResult
			bestTerm := 1

			for _, opt := range options {
				var pr *planResult
				if opt.isVar {
					pr = getVar(dpDate)
				} else {
					pr = getFixed(opt.termMonths, dpDate)
				}
				if pr == nil {
					continue
				}
				termLen := opt.termMonths
				endDate := dpDate.AddDate(0, termLen, 0)
				cost, covered := costForDateRange(pr.ap, dpDate, endDate)
				if covered <= 0 {
					continue
				}
				costPerPeriod := cost / covered
				if costPerPeriod < bestCostPerPeriod {
					bestCostPerPeriod = costPerPeriod
					bestPR = pr
					bestTerm = termLen
				}
			}

			if bestPR == nil {
				break
			}
			nextDate := dpDate.AddDate(0, bestTerm, 0)
			segEnd := nextDate
			if segEnd.After(windowEnd) {
				segEnd = windowEnd
			}
			segs = append(segs, planSegment{start: dpDate, end: segEnd, ap: bestPR.ap})
			switches = append(switches, SwitchEvent{
				EffectivePeriod: dateToPeriod(dpDate),
				ETFPaid:        0,
				Plan:           bestPR.info,
			})
			dpDate = nextDate
		}
		results = append(results, buildResult("optimal_greedy", "Optimal — greedy at each decision point", segs, switches, 0))
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
