package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Plan is a candidate plan from the database, enriched with decomposed rates.
// isActual is unexported: true when rates come from today's live plans, false for historical projections.
type Plan struct {
	RepCompany   string  `json:"rep_company"`
	Product      string  `json:"product"`
	TermValue    int     `json:"term_value"`
	RateType     string  `json:"rate_type"`
	BaseFee      float64 `json:"base_fee"`      // $ per month (decomposed)
	PerKwhRate   float64 `json:"per_kwh_rate"`  // ¢/kWh (decomposed)
	EnrollURL    string  `json:"enroll_url"`
	isActual     bool    // not serialised; set by selectBestPlan
	Kwh1000Cents float64 `json:"kwh1000_cents"` // original kwh1000 from db (¢/kWh all-in at 1000 kWh)
}

type ProjectionRequest struct {
	CurrentRateCents   float64 `json:"current_rate_cents"`
	CurrentBaseFee     float64 `json:"current_base_fee"`
	ETFAmount          float64 `json:"etf_amount"`
	ContractExpiration string  `json:"contract_expiration"`
}

type SwitchEvent struct {
	EffectivePeriod string  `json:"effective_period"` // "T+N" period label
	ETFPaid         float64 `json:"etf_paid"`
	Plan            Plan    `json:"plan"`
}

type PeriodBreakdown struct {
	Period           string  `json:"period"`            // "T+N" period label
	PeriodStart      string  `json:"period_start"`      // "YYYY-MM-DD"
	PeriodEnd        string  `json:"period_end"`        // "YYYY-MM-DD" (inclusive last day)
	UsageKwh         float64 `json:"usage_kwh"`
	UsageIsEstimated bool    `json:"usage_is_estimated"`
	ActivePlan       Plan    `json:"active_plan"`
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
	plan  Plan
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

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// projectionContext holds the shared state used across the projection computation.
type projectionContext struct {
	today   time.Time
	allPlans map[string][]Plan
	usageMap        map[int]float64
	estimatedMap    map[int]bool
	avgUsage        float64
	numPeriods      int
	periodStarts    []time.Time
	windowStart     time.Time
	windowEnd       time.Time
}

// usageForPeriod returns (usage kWh, isEstimated) for the given period index.
// Falls back to avgUsage when no historical data exists.
func (pc *projectionContext) usageForPeriod(periodIdx int) (float64, bool) {
	u, ok := pc.usageMap[periodIdx]
	if !ok || u == 0 {
		return pc.avgUsage, true
	}
	return u, pc.estimatedMap[periodIdx]
}

// dateToPeriod maps a calendar date to the T+N label of the period it falls in.
func (pc *projectionContext) dateToPeriod(d time.Time) string {
	for i := 0; i < pc.numPeriods; i++ {
		if !d.Before(pc.periodStarts[i]) && d.Before(pc.periodStarts[i+1]) {
			return periodLabel(i)
		}
	}
	return periodLabel(pc.numPeriods)
}

// bestPlanInRange finds the cheapest plan within the inclusive date range [start, end]
// with the given term. Only plans whose TermValue matches termMonths are considered.
// Returns nil if no matching plan is found in the range.
func (pc *projectionContext) bestPlanInRange(
	termMonths int,
	numCoveredPeriods int, totalUsage float64,
	start, end time.Time,
) *Plan {
	bestCost := math.MaxFloat64
	var best *Plan
	for dateStr, candidates := range pc.allPlans {
		fetchDate, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr != nil || fetchDate.Before(start) || fetchDate.After(end) {
			continue
		}
		for i := range candidates {
			r := &candidates[i]
			if r.TermValue != termMonths {
				continue
			}
			cost := float64(numCoveredPeriods)*r.BaseFee + totalUsage*r.PerKwhRate/100.0
			if cost < bestCost {
				bestCost = cost
				best = r
			}
		}
	}
	return best
}

// selectBestPlan finds the cheapest plan for decisionDate with the given term.
// termMonths == 1 selects variable plans; termMonths > 1 selects fixed plans.
//
// Phase 1 (within the 30-day enrollment window): searches today's plans via
// bestPlanInRange(today, today).
//
// Phase 2 (always runs, overrides if cheaper): searches a historical range
// anchored one year before decisionDate. Within the enrollment window the range
// is [today−1yr+1d, decisionDate−1yr]; outside it is [decisionDate−1yr−30d,
// decisionDate−1yr]. Falls back to the most recent available date when no data
// exists in the ideal window.
//
// Returns nil if no data exists from either source. The returned Plan copy has
// isActual set appropriately.
func (pc *projectionContext) selectBestPlan(termMonths int, decisionDate time.Time) *Plan {
	termEnd := decisionDate.AddDate(0, termMonths, 0)

	termUsage, numTermPeriods := 0.0, 0
	for j := 0; j < pc.numPeriods; j++ {
		if !periodCoversSegment(pc.periodStarts[j], pc.periodStarts[j+1], decisionDate, termEnd) {
			continue
		}
		usageKwh, _ := pc.usageForPeriod(j)
		termUsage += usageKwh
		numTermPeriods++
	}
	if numTermPeriods == 0 {
		numTermPeriods = termMonths
	}

	bestCost := math.MaxFloat64
	var bestPlan *Plan
	isActual := false

	inEnrollmentWindow := !pc.today.Before(decisionDate.AddDate(0, 0, -30))

	// Phase 1: today's live plans — only within the 30-day enrollment window.
	if inEnrollmentWindow {
		if p := pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, pc.today, pc.today); p != nil {
			cost := float64(numTermPeriods)*p.BaseFee + termUsage*p.PerKwhRate/100.0
			if cost < bestCost {
				bestCost = cost
				bestPlan = p
				isActual = true
			}
		}
	}

	// Phase 2: historical range — always runs, overrides if cheaper.
	// When decisionDate == today the range is inverted (histStart > histEnd) so
	// today's live plans (phase 1) are the sole source.
	var histStart, histEnd time.Time
	if inEnrollmentWindow {
		histStart = pc.today.AddDate(-1, 0, 1)
		histEnd = decisionDate.AddDate(-1, 0, 0)
	} else {
		histStart = decisionDate.AddDate(-1, 0, -30)
		histEnd = decisionDate.AddDate(-1, 0, 0)
	}
	if histStart.Before(histEnd) {
		histPlan := pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, histStart, histEnd)
		if histPlan == nil {
			// Fallback: no data in ideal window — use the most recent date that has
			// at least one plan with a matching term.
			var latestDate time.Time
			for dateStr, candidates := range pc.allPlans {
				fetchDate, parseErr := time.Parse("2006-01-02", dateStr)
				if parseErr != nil {
					continue
				}
				for _, r := range candidates {
					if r.TermValue == termMonths && fetchDate.After(latestDate) {
						latestDate = fetchDate
						break
					}
				}
			}
			if !latestDate.IsZero() {
				histPlan = pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, latestDate, latestDate)
			}
		}
		if histPlan != nil {
			histCost := float64(numTermPeriods)*histPlan.BaseFee + termUsage*histPlan.PerKwhRate/100.0
			if histCost < bestCost {
				bestCost = histCost
				bestPlan = histPlan
				isActual = false
			}
		}
	}

	if bestPlan == nil {
		return nil
	}

	// Return a copy with isActual stamped in.
	result := *bestPlan
	result.isActual = isActual
	return &result
}

// buildBreakdown computes per-period costs for the given plan segments.
// For each T+x period, finds which segment covers it and computes cost.
// All segment boundaries align with period boundaries so each period is
// covered by exactly one segment.
func (pc *projectionContext) buildBreakdown(segments []planSegment) ([]PeriodBreakdown, float64) {
	breakdown := make([]PeriodBreakdown, pc.numPeriods)
	total := 0.0
	for i := 0; i < pc.numPeriods; i++ {
		periodStart := pc.periodStarts[i]
		periodEnd := pc.periodStarts[i+1]
		usageKwh, isEstimated := pc.usageForPeriod(i)

		var segPlan Plan
		for _, seg := range segments {
			if !periodCoversSegment(periodStart, periodEnd, seg.start, seg.end) {
				continue
			}
			segPlan = seg.plan
			break // each period is covered by exactly one segment
		}

		cost := segPlan.BaseFee + usageKwh*segPlan.PerKwhRate/100.0
		total += cost
		// period_end is shown as the last day (inclusive) = periodEnd - 1 day
		lastDay := periodEnd.AddDate(0, 0, -1)
		breakdown[i] = PeriodBreakdown{
			Period:           periodLabel(i),
			PeriodStart:      periodStart.Format("2006-01-02"),
			PeriodEnd:        lastDay.Format("2006-01-02"),
			UsageKwh:         round2(usageKwh),
			UsageIsEstimated: isEstimated,
			ActivePlan:       segPlan,
			RateCents:        round2(segPlan.PerKwhRate),
			BaseFee:          round2(segPlan.BaseFee),
			PeriodCost:       round2(cost),
			Confidence:       periodConfidence(periodStart, pc.today),
			IsProjected:      !segPlan.isActual,
		}
	}
	return breakdown, total
}

// buildResult assembles a StrategyResult from segments and switches.
func (pc *projectionContext) buildResult(id, name string, segments []planSegment, switches []SwitchEvent, etfPaid float64) StrategyResult {
	breakdown, total := pc.buildBreakdown(segments)
	confidence := "high"
	for _, switchEvt := range switches {
		for i := 0; i < pc.numPeriods; i++ {
			if periodLabel(i) == switchEvt.EffectivePeriod {
				confidence = lowestConfidence(confidence, periodConfidence(pc.periodStarts[i], pc.today))
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

// buildFixedRolling builds plan segments for a rolling fixed-term strategy starting at T = windowStart.
// firstDecisionDate: the date used to select the first plan's rates.
//   - "at expiry" strategies pass windowStart (use projected/historical rates).
//   - "switch now" strategies pass today (lock in today's live rates for T).
//
// Subsequent terms always select rates at their own decision date.
func (pc *projectionContext) buildFixedRolling(termMonths int, initialETF float64, firstDecisionDate time.Time) ([]planSegment, []SwitchEvent) {
	var segments []planSegment
	var switches []SwitchEvent
	isFirst := true
	for decisionDate := pc.windowStart; decisionDate.Before(pc.windowEnd); {
		selectDate := decisionDate
		etf := 0.0
		if isFirst {
			selectDate = firstDecisionDate
			etf = initialETF
			isFirst = false
		}
		actualTerm := termMonths
		planRes := pc.selectBestPlan(termMonths, selectDate)
		if planRes == nil {
			planRes = pc.selectBestPlan(1, selectDate)
			actualTerm = 1
		}
		if planRes == nil {
			break
		}
		segEnd := decisionDate.AddDate(0, actualTerm, 0)
		if segEnd.After(pc.windowEnd) {
			segEnd = pc.windowEnd
		}
		segments = append(segments, planSegment{start: decisionDate, end: segEnd, plan: *planRes})
		switches = append(switches, SwitchEvent{
			EffectivePeriod: pc.dateToPeriod(decisionDate),
			ETFPaid:         etf,
			Plan:            *planRes,
		})
		decisionDate = decisionDate.AddDate(0, actualTerm, 0)
	}
	return segments, switches
}

// costForDateRange sums the projected cost for the given Plan over all
// periods that fall within [startDate, endDate). Returns (totalCost, periodsCovered).
func (pc *projectionContext) costForDateRange(plan Plan, startDate, endDate time.Time) (float64, int) {
	totalCost := 0.0
	periodsCovered := 0
	for i := 0; i < pc.numPeriods; i++ {
		periodStart := pc.periodStarts[i]
		periodEnd := pc.periodStarts[i+1]
		if !periodCoversSegment(periodStart, periodEnd, startDate, endDate) {
			continue
		}
		usageKwh, _ := pc.usageForPeriod(i)
		totalCost += plan.BaseFee + usageKwh*plan.PerKwhRate/100.0
		periodsCovered++
	}
	return totalCost, periodsCovered
}

// newProjectionContext builds a projectionContext anchored at the given windowStart.
// allPlans is pre-fetched and shared across contexts.
func newProjectionContext(
	ctx context.Context,
	pool *pgxpool.Pool,
	today time.Time,
	windowStart time.Time,
	allPlans map[string][]Plan,
) (*projectionContext, error) {
	const numPeriods = 12

	// periodStarts[i] = windowStart + i months; periodStarts[12] = windowEnd
	periodStarts := make([]time.Time, numPeriods+1)
	for i := 0; i <= numPeriods; i++ {
		periodStarts[i] = windowStart.AddDate(0, i, 0)
	}
	windowEnd := periodStarts[numPeriods]

	// Historical period starts for usage lookup: same T+i offsets, 1 year back.
	histPeriodStarts := make([]time.Time, numPeriods)
	for i := 0; i < numPeriods; i++ {
		histPeriodStarts[i] = windowStart.AddDate(-1, i, 0)
	}

	usageMap, estimatedMap, err := queryPeriodUsage(ctx, pool, histPeriodStarts)
	if err != nil {
		return nil, fmt.Errorf("queryPeriodUsage: %w", err)
	}

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

	return &projectionContext{
		today:    today,
		allPlans: allPlans,
		usageMap: usageMap,
		estimatedMap:    estimatedMap,
		avgUsage:        avgUsage,
		numPeriods:      numPeriods,
		periodStarts:    periodStarts,
		windowStart:     windowStart,
		windowEnd:       windowEnd,
	}, nil
}

func computeProjection(ctx context.Context, pool *pgxpool.Pool, req ProjectionRequest, today time.Time) ([]StrategyResult, error) {
	expiry, err := time.Parse("2006-01-02", req.ContractExpiration)
	if err != nil {
		return nil, fmt.Errorf("invalid contract_expiration: %w", err)
	}
	expiry = time.Date(expiry.Year(), expiry.Month(), expiry.Day(), 0, 0, 0, 0, time.UTC)

	// windowStartExpiry is used by "at expiry" strategies:
	//   - expiry date if it's in the future
	//   - today if contract has already expired
	windowStartExpiry := expiry
	if expiry.Before(today) {
		windowStartExpiry = today
	}

	// windowStartNow is used by "switch now" strategies: the window begins today.
	windowStartNow := today

	// Fetch all plans (all dates) in one query.
	allPlans, err := queryAllDecomposedPlans(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("queryAllDecomposedPlans: %w", err)
	}

	// Build one context per window-start: period boundaries, usage lookups, and
	// average-usage fallbacks all depend on where the window begins.
	pcExpiry, err := newProjectionContext(ctx, pool, today, windowStartExpiry, allPlans)
	if err != nil {
		return nil, err
	}
	pcNow, err := newProjectionContext(ctx, pool, today, windowStartNow, allPlans)
	if err != nil {
		return nil, err
	}

	// ETF applies if switching today is before (expiry − 14 days).
	etfCutoff := expiry.AddDate(0, 0, -14)
	etfOnSwitchNow := 0.0
	if today.Before(etfCutoff) {
		etfOnSwitchNow = req.ETFAmount
	}

	var results []StrategyResult

	// ── 1. BASELINE ───────────────────────────────────────────────────────────
	// From expiry, switch to the best variable (1-month) plan every month.
	{
		segments, switches := pcExpiry.buildFixedRolling(1, 0, windowStartExpiry)
		results = append(results, pcExpiry.buildResult("baseline", "Baseline — best variable monthly from expiry", segments, switches, 0))
	}

	// ── 2. SWITCH_AT_EXPIRY_12M ───────────────────────────────────────────────
	{
		segments, switches := pcExpiry.buildFixedRolling(12, 0, windowStartExpiry)
		results = append(results, pcExpiry.buildResult("switch_at_expiry_12m", "Switch at expiry — 12-month fixed", segments, switches, 0))
	}

	// ── 3. SWITCH_AT_EXPIRY_6M ────────────────────────────────────────────────
	{
		segments, switches := pcExpiry.buildFixedRolling(6, 0, windowStartExpiry)
		results = append(results, pcExpiry.buildResult("switch_at_expiry_6m", "Switch at expiry — 6-month rolling", segments, switches, 0))
	}

	// ── 4. SWITCH_AT_EXPIRY_3M ────────────────────────────────────────────────
	{
		segments, switches := pcExpiry.buildFixedRolling(3, 0, windowStartExpiry)
		results = append(results, pcExpiry.buildResult("switch_at_expiry_3m", "Switch at expiry — 3-month rolling", segments, switches, 0))
	}

	// ── 5. SWITCH_NOW_12M ─────────────────────────────────────────────────────
	// Window starts today; period boundaries and usage are anchored to today.
	// firstDecisionDate == windowStart == today, so today's live rates cover T+1.
	{
		segments, switches := pcNow.buildFixedRolling(12, etfOnSwitchNow, today)
		results = append(results, pcNow.buildResult("switch_now_12m", "Switch now — 12-month fixed", segments, switches, etfOnSwitchNow))
	}

	// ── 6. SWITCH_NOW_3M ──────────────────────────────────────────────────────
	{
		segments, switches := pcNow.buildFixedRolling(3, etfOnSwitchNow, today)
		results = append(results, pcNow.buildResult("switch_now_3m", "Switch now — 3-month rolling", segments, switches, etfOnSwitchNow))
	}

	// ── 7. SWITCH_NOW_6M ──────────────────────────────────────────────────────
	{
		segments, switches := pcNow.buildFixedRolling(6, etfOnSwitchNow, today)
		results = append(results, pcNow.buildResult("switch_now_6m", "Switch now — 6-month rolling", segments, switches, etfOnSwitchNow))
	}

	// ── 8. SWITCH_AT_EXPIRY_3M_OR_4M ─────────────────────────────────────────
	// At each decision point, pick the cheaper of 3-month and 4-month plans
	// (compared by cost-per-period over the remaining window). Falls back to
	// 1-month variable if neither is available.
	{
		var segments []planSegment
		var switches []SwitchEvent

		preferredTerms := []int{3, 4}

		decisionDate := windowStartExpiry
		for decisionDate.Before(pcExpiry.windowEnd) {
			bestCostPerPeriod := math.MaxFloat64
			var bestPlanRes *Plan
			bestTermMonths := 1

			for _, termMonths := range preferredTerms {
				planRes := pcExpiry.selectBestPlan(termMonths, decisionDate)
				if planRes == nil {
					continue
				}
				totalCost, periodsCovered := pcExpiry.costForDateRange(*planRes, decisionDate, pcExpiry.windowEnd)
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

			// Fallback to 1-month variable if no 3m/4m plan is available.
			if bestPlanRes == nil {
				bestPlanRes = pcExpiry.selectBestPlan(1, decisionDate)
				bestTermMonths = 1
			}
			if bestPlanRes == nil {
				break
			}

			nextDate := decisionDate.AddDate(0, bestTermMonths, 0)
			segEnd := nextDate
			if segEnd.After(pcExpiry.windowEnd) {
				segEnd = pcExpiry.windowEnd
			}
			segments = append(segments, planSegment{start: decisionDate, end: segEnd, plan: *bestPlanRes})
			switches = append(switches, SwitchEvent{
				EffectivePeriod: pcExpiry.dateToPeriod(decisionDate),
				ETFPaid:         0,
				Plan:            *bestPlanRes,
			})
			decisionDate = nextDate
		}
		results = append(results, pcExpiry.buildResult("switch_at_expiry_3m_or_4m", "Switch at expiry — best 3 or 4-month rolling", segments, switches, 0))
	}

	// ── 9. OPTIMAL_GREEDY ─────────────────────────────────────────────────────
	// At each decision point (starting from windowStartExpiry), pick the term
	// that minimises projected cost-per-period for the remaining window.
	{
		var segments []planSegment
		var switches []SwitchEvent

		termOptions := []int{1, 3, 6, 12}

		decisionDate := windowStartExpiry
		for decisionDate.Before(pcExpiry.windowEnd) {
			bestCostPerPeriod := math.MaxFloat64
			var bestPlanRes *Plan
			bestTermMonths := 1

			for _, termMonths := range termOptions {
				planRes := pcExpiry.selectBestPlan(termMonths, decisionDate)
				if planRes == nil {
					continue
				}
				// Evaluate each option over the full remaining window so that
				// all terms are compared on the same horizon. Using only the
				// term's own duration biases the comparison toward shorter
				// terms: a 1-month variable plan only needs to beat a
				// 12-month average to win, letting it dominate every period.
				totalCost, periodsCovered := pcExpiry.costForDateRange(*planRes, decisionDate, pcExpiry.windowEnd)
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
			if segEnd.After(pcExpiry.windowEnd) {
				segEnd = pcExpiry.windowEnd
			}
			segments = append(segments, planSegment{start: decisionDate, end: segEnd, plan: *bestPlanRes})
			switches = append(switches, SwitchEvent{
				EffectivePeriod: pcExpiry.dateToPeriod(decisionDate),
				ETFPaid:         0,
				Plan:            *bestPlanRes,
			})
			decisionDate = nextDate
		}
		results = append(results, pcExpiry.buildResult("optimal_greedy", "Optimal — greedy at each decision point", segments, switches, 0))
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
