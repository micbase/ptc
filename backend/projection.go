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
	EffectiveMonth string             `json:"effective_month"`
	ETFPaid        float64            `json:"etf_paid"`
	Plan           ProjectionPlanInfo `json:"plan"`
}

type MonthlyBreakdown struct {
	Month            string  `json:"month"`
	UsageKwh         float64 `json:"usage_kwh"`
	UsageIsEstimated bool    `json:"usage_is_estimated"`
	ActivePlanLabel  string  `json:"active_plan_label"`
	RateCents        float64 `json:"rate_cents"`
	BaseFee          float64 `json:"base_fee"`
	MonthlyCost      float64 `json:"monthly_cost"`
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
	MonthlyBreakdown       []MonthlyBreakdown `json:"monthly_breakdown"`
}

// activePlan holds the effective rates for a given month in a strategy.
type activePlan struct {
	label     string
	rateCents float64
	baseFee   float64
}

func monthLabel(t time.Time) string {
	return t.Format("2006-01")
}

func monthConfidence(idx int) string {
	if idx < 2 {
		return "high"
	} else if idx < 6 {
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

// seasonalRatio computes the seasonal adjustment factor for a future decision month.
// Uses historical minimum rates: same-month-last-year / today's-month-last-year.
func seasonalRatio(decisionMonth, today time.Time, rates map[string]float64) float64 {
	sameLY := time.Date(decisionMonth.Year()-1, decisionMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	todayLY := time.Date(today.Year()-1, today.Month(), 1, 0, 0, 0, 0, time.UTC)
	num, ok1 := rates[monthLabel(sameLY)]
	den, ok2 := rates[monthLabel(todayLY)]
	if !ok1 || !ok2 || den == 0 {
		return 1.0
	}
	return num / den
}

// bestFixedPlanForTerm returns the plan with the lowest PerKwhRate for the given term.
func bestFixedPlanForTerm(plans []LinearPlan, term int) *LinearPlan {
	var best *LinearPlan
	for i := range plans {
		p := &plans[i]
		if p.RateType == "Variable" || p.TermValue != term {
			continue
		}
		if best == nil || p.PerKwhRate < best.PerKwhRate {
			best = p
		}
	}
	return best
}

// bestVariablePlan returns the variable plan with the lowest PerKwhRate.
func bestVariablePlan(plans []LinearPlan) *LinearPlan {
	var best *LinearPlan
	for i := range plans {
		p := &plans[i]
		if p.RateType != "Variable" {
			continue
		}
		if best == nil || p.PerKwhRate < best.PerKwhRate {
			best = p
		}
	}
	return best
}

type planResult struct {
	ap   activePlan
	info ProjectionPlanInfo
}

// round2 rounds to 2 decimal places.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func computeProjection(ctx context.Context, pool *pgxpool.Pool, req ProjectionRequest, today time.Time) ([]StrategyResult, error) {
	// 1. Query linear plans for today
	linearPlans, err := queryLinearPlans(ctx, pool, today)
	if err != nil {
		return nil, fmt.Errorf("queryLinearPlans: %w", err)
	}

	// 2. Query monthly usage (1 year prior)
	usageMap, estimatedMap, err := queryMonthlyUsage(ctx, pool, today)
	if err != nil {
		return nil, fmt.Errorf("queryMonthlyUsage: %w", err)
	}

	// 3. Query historical min rates for seasonal ratio
	fixedRates, err := queryHistoricalMinRates(ctx, pool, false)
	if err != nil {
		return nil, fmt.Errorf("queryHistoricalMinRates(fixed): %w", err)
	}
	varRates, err := queryHistoricalMinRates(ctx, pool, true)
	if err != nil {
		return nil, fmt.Errorf("queryHistoricalMinRates(variable): %w", err)
	}

	// 4. Build 12-month window starting from today's month
	months := make([]time.Time, 12)
	for i := 0; i < 12; i++ {
		months[i] = time.Date(today.Year(), today.Month()+time.Month(i), 1, 0, 0, 0, 0, time.UTC)
	}

	// 5. Parse contract expiration
	expiry, err := time.Parse("2006-01-02", req.ContractExpiration)
	if err != nil {
		return nil, fmt.Errorf("invalid contract_expiration: %w", err)
	}

	// firstFreeIdx: index of first month where a new plan can start.
	// Current plan covers any month whose first day is on or before the expiry date.
	firstFreeIdx := 0
	for i, m := range months {
		if !m.After(expiry) {
			firstFreeIdx = i + 1
		}
	}
	if firstFreeIdx > 12 {
		firstFreeIdx = 12
	}

	// ETF applies if switching today is before (expiry - 14 days)
	etfCutoff := expiry.AddDate(0, 0, -14)
	etfOnSwitchNow := 0.0
	if today.Before(etfCutoff) {
		etfOnSwitchNow = req.ETFAmount
	}

	// Average usage for fallback
	totalUsage, usageCount := 0.0, 0
	for _, m := range months {
		if u, ok := usageMap[monthLabel(m)]; ok && u > 0 {
			totalUsage += u
			usageCount++
		}
	}
	avgUsage := 500.0
	if usageCount > 0 {
		avgUsage = totalUsage / float64(usageCount)
	}

	usage := func(idx int) (float64, bool) {
		ml := monthLabel(months[idx])
		u, ok := usageMap[ml]
		if !ok || u == 0 {
			return avgUsage, true // estimated
		}
		return u, estimatedMap[ml]
	}

	// projectFixed: project a fixed plan's rates to a decision month
	projectFixed := func(p *LinearPlan, dp time.Time) (rateCents, baseFee float64) {
		r := seasonalRatio(dp, today, fixedRates)
		return p.PerKwhRate * r, p.BaseFee * r
	}
	// projectVar: project a variable plan's rates to a decision month
	projectVar := func(p *LinearPlan, dp time.Time) (rateCents, baseFee float64) {
		r := seasonalRatio(dp, today, varRates)
		return p.PerKwhRate * r, p.BaseFee * r
	}

	// getPlanResult builds a planResult for a fixed plan at a given decision month
	getFixed := func(term int, dp time.Time) *planResult {
		p := bestFixedPlanForTerm(linearPlans, term)
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
				IDKey:              p.IDKey,
				RepCompany:         p.RepCompany,
				Product:            p.Product,
				TermValue:          p.TermValue,
				RateType:           p.RateType,
				ProjectedRateCents: rc,
				ProjectedBaseFee:   bf,
				Renewable:          p.Renewable,
				Rating:             p.Rating,
				EnrollURL:          p.EnrollURL,
			},
		}
	}

	getVar := func(dp time.Time) *planResult {
		p := bestVariablePlan(linearPlans)
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
				IDKey:              p.IDKey,
				RepCompany:         p.RepCompany,
				Product:            p.Product,
				TermValue:          p.TermValue,
				RateType:           "Variable",
				ProjectedRateCents: rc,
				ProjectedBaseFee:   bf,
				Renewable:          p.Renewable,
				Rating:             p.Rating,
				EnrollURL:          p.EnrollURL,
			},
		}
	}

	// currentAP is the current plan as an activePlan
	currentAP := activePlan{
		label:     "Current plan",
		rateCents: req.CurrentRateCents,
		baseFee:   req.CurrentBaseFee,
	}

	// buildMonthlyBreakdown constructs the monthly breakdown from plans array
	buildMonthlyBreakdown := func(plans [12]activePlan) ([]MonthlyBreakdown, float64) {
		bd := make([]MonthlyBreakdown, 12)
		total := 0.0
		for i, m := range months {
			u, isEst := usage(i)
			p := plans[i]
			cost := p.baseFee + u*p.rateCents/100.0
			total += cost
			bd[i] = MonthlyBreakdown{
				Month:            monthLabel(m),
				UsageKwh:         round2(u),
				UsageIsEstimated: isEst,
				ActivePlanLabel:  p.label,
				RateCents:        round2(p.rateCents),
				BaseFee:          round2(p.baseFee),
				MonthlyCost:      round2(cost),
				Confidence:       monthConfidence(i),
			}
		}
		return bd, total
	}

	// buildResult assembles a StrategyResult
	buildResult := func(id, name string, plans [12]activePlan, switches []SwitchEvent, etfPaid float64) StrategyResult {
		bd, total := buildMonthlyBreakdown(plans)

		// Strategy confidence = lowest confidence among switch months
		conf := "high"
		for _, sw := range switches {
			for i, m := range months {
				if monthLabel(m) == sw.EffectiveMonth {
					conf = lowestConfidence(conf, monthConfidence(i))
				}
			}
		}

		if switches == nil {
			switches = []SwitchEvent{}
		}

		return StrategyResult{
			StrategyID:       id,
			StrategyName:     name,
			TotalCost:        round2(total),
			ETFPaid:          etfPaid,
			SwitchCount:      len(switches),
			Confidence:       conf,
			Switches:         switches,
			MonthlyBreakdown: bd,
		}
	}

	// perMonthCost computes projected cost per month for a plan over a range [start, end)
	perMonthCost := func(ap activePlan, startIdx, endIdx int) float64 {
		if endIdx <= startIdx {
			return math.MaxFloat64
		}
		total := 0.0
		for i := startIdx; i < endIdx; i++ {
			u, _ := usage(i)
			total += ap.baseFee + u*ap.rateCents/100.0
		}
		return total / float64(endIdx-startIdx)
	}

	var results []StrategyResult

	// ── 1. BASELINE ──────────────────────────────────────────────────────────────
	{
		var plans [12]activePlan
		for i := range plans {
			if i < firstFreeIdx {
				plans[i] = currentAP
			} else {
				// Roll to best variable at that specific month
				vp := getVar(months[i])
				if vp != nil {
					plans[i] = vp.ap
				} else {
					plans[i] = currentAP
				}
			}
		}
		results = append(results, buildResult("baseline", "Baseline — stay on current, roll to variable at expiry", plans, nil, 0))
	}

	// Helper: fill from startIdx onwards with a rolling plan of given term length,
	// picking the best plan (fixed term or variable) at each decision point.
	buildRollingStrategy := func(startIdx, termMonths int, isVar bool, dp0ETF float64) ([12]activePlan, []SwitchEvent) {
		var plans [12]activePlan
		var switches []SwitchEvent

		// Fill current plan for months before startIdx
		for i := 0; i < startIdx && i < 12; i++ {
			plans[i] = currentAP
		}

		dpIdx := startIdx
		firstSwitch := true
		for dpIdx < 12 {
			dp := months[dpIdx]
			endIdx := dpIdx + termMonths
			if isVar {
				endIdx = dpIdx + 1
			}

			var pr *planResult
			if isVar {
				pr = getVar(dp)
			} else {
				pr = getFixed(termMonths, dp)
				if pr == nil {
					pr = getVar(dp) // fallback to variable
				}
			}

			etfThisSwitch := 0.0
			if firstSwitch {
				etfThisSwitch = dp0ETF
				firstSwitch = false
			}

			if pr != nil {
				sw := SwitchEvent{
					EffectiveMonth: monthLabel(dp),
					ETFPaid:        etfThisSwitch,
					Plan:           pr.info,
				}
				switches = append(switches, sw)
				for i := dpIdx; i < endIdx && i < 12; i++ {
					plans[i] = pr.ap
				}
			} else {
				for i := dpIdx; i < endIdx && i < 12; i++ {
					plans[i] = currentAP
				}
			}
			dpIdx = endIdx
		}
		return plans, switches
	}

	// ── 2. SWITCH_AT_EXPIRY_12M ───────────────────────────────────────────────
	{
		plans, switches := buildRollingStrategy(firstFreeIdx, 12, false, 0)
		results = append(results, buildResult("switch_at_expiry_12m", "Switch at expiry — 12-month fixed", plans, switches, 0))
	}

	// ── 3. SWITCH_AT_EXPIRY_6M ────────────────────────────────────────────────
	{
		plans, switches := buildRollingStrategy(firstFreeIdx, 6, false, 0)
		results = append(results, buildResult("switch_at_expiry_6m", "Switch at expiry — 6-month rolling", plans, switches, 0))
	}

	// ── 4. SWITCH_AT_EXPIRY_3M ────────────────────────────────────────────────
	{
		plans, switches := buildRollingStrategy(firstFreeIdx, 3, false, 0)
		results = append(results, buildResult("switch_at_expiry_3m", "Switch at expiry — 3-month rolling", plans, switches, 0))
	}

	// ── 5. SWITCH_NOW_12M ─────────────────────────────────────────────────────
	{
		plans, switches := buildRollingStrategy(0, 12, false, etfOnSwitchNow)
		results = append(results, buildResult("switch_now_12m", "Switch now — 12-month fixed", plans, switches, etfOnSwitchNow))
	}

	// ── 6. SWITCH_NOW_3M ──────────────────────────────────────────────────────
	{
		plans, switches := buildRollingStrategy(0, 3, false, etfOnSwitchNow)
		results = append(results, buildResult("switch_now_3m", "Switch now — 3-month rolling", plans, switches, etfOnSwitchNow))
	}

	// ── 7. OPTIMAL_GREEDY ────────────────────────────────────────────────────
	{
		var plans [12]activePlan
		var switches []SwitchEvent

		for i := 0; i < firstFreeIdx && i < 12; i++ {
			plans[i] = currentAP
		}

		type termOption struct {
			term  int
			isVar bool
		}
		options := []termOption{
			{0, true}, // variable (1-month rolling)
			{3, false},
			{6, false},
			{12, false},
		}

		dpIdx := firstFreeIdx
		for dpIdx < 12 {
			dp := months[dpIdx]
			remaining := 12 - dpIdx

			bestCPM := math.MaxFloat64
			var bestPR *planResult
			bestTermLen := 1

			for _, opt := range options {
				var pr *planResult
				if opt.isVar {
					pr = getVar(dp)
				} else {
					pr = getFixed(opt.term, dp)
				}
				if pr == nil {
					continue
				}

				termLen := opt.term
				if opt.isVar {
					termLen = 1
				}
				endIdx := min(dpIdx+termLen, 12)
				cpm := perMonthCost(pr.ap, dpIdx, endIdx)
				if cpm < bestCPM {
					bestCPM = cpm
					bestPR = pr
					bestTermLen = termLen
				}
			}

			if bestPR == nil {
				// No plan found; fill remaining with current plan
				for i := dpIdx; i < 12; i++ {
					plans[i] = currentAP
				}
				break
			}

			_ = remaining
			endIdx := min(dpIdx+bestTermLen, 12)
			switches = append(switches, SwitchEvent{
				EffectiveMonth: monthLabel(dp),
				ETFPaid:        0,
				Plan:           bestPR.info,
			})
			for i := dpIdx; i < endIdx; i++ {
				plans[i] = bestPR.ap
			}
			dpIdx = endIdx
		}

		results = append(results, buildResult("optimal_greedy", "Optimal — greedy at each decision point", plans, switches, 0))
	}

	// Compute savings vs baseline for all strategies
	baselineCost := results[0].TotalCost
	for i := range results {
		savings := round2(baselineCost - results[i].TotalCost)
		results[i].TotalSavingsVsBaseline = savings
		results[i].NetSavings = round2(savings - results[i].ETFPaid)
	}

	return results, nil
}
