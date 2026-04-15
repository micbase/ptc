package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanKind describes the source of a plan's rates.
// actual    = today's live market rates (enrollment is available now).
// projected = historical rates from ~1 year ago used as a proxy for a future period.
// fallback  = most-recent available historical rates, used when the ideal window has no data.
type PlanKind string

const (
	PlanKindActual    PlanKind = "actual"
	PlanKindProjected PlanKind = "projected"
	PlanKindFallback  PlanKind = "fallback"
)

// Plan is a candidate plan from the database, enriched with decomposed rates.
// planKind is unexported; set by selectBestPlan to indicate the data source.
type Plan struct {
	ElectricityRateID int      `json:"electricity_rate_id"` // electricity_rates.id, for recording switch events
	RepCompany        string   `json:"rep_company"`
	Product           string   `json:"product"`
	TermValue         int      `json:"term_value"`
	RateType          string   `json:"rate_type"`
	BaseFee           float64  `json:"base_fee"`      // $ per month (decomposed)
	PerKwhRate        float64  `json:"per_kwh_rate"`  // ¢/kWh (decomposed)
	EnrollURL         string   `json:"enroll_url"`
	planKind          PlanKind // not serialised; set by selectBestPlan
	Kwh1000Cents      float64  `json:"kwh1000_cents"` // original kwh1000 from db (¢/kWh all-in at 1000 kWh)
}

// monthsBetween returns the number of months from a to b, computed as ceiling(days/30).
// For example, 31 days = 1 month + 1 day → returns 2.
func monthsBetween(a, b time.Time) int {
	days := int(b.Sub(a).Hours() / 24)
	if days <= 0 {
		return 0
	}
	return (days + 29) / 30 // ceiling division by 30
}

type SwitchEvent struct {
	EffectivePeriod string  `json:"effective_period"` // "T+N" period label
	ETFPaid         float64 `json:"etf_paid"`
	Plan            Plan    `json:"plan"`
}

type PeriodBreakdown struct {
	Period           string   `json:"period"`            // "T+N" period label
	PeriodStart      string   `json:"period_start"`      // "YYYY-MM-DD"
	PeriodEnd        string   `json:"period_end"`        // "YYYY-MM-DD" (inclusive last day)
	UsageKwh         float64  `json:"usage_kwh"`
	UsageIsEstimated bool     `json:"usage_is_estimated"`
	ActivePlan       Plan     `json:"active_plan"`
	RateCents        float64  `json:"rate_cents"`
	BaseFee          float64  `json:"base_fee"`
	PeriodCost       float64  `json:"period_cost"`
	PlanKind         PlanKind `json:"plan_kind"` // "actual" | "projected" | "fallback"
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

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// projectionContext holds the shared state used across the projection computation.
type projectionContext struct {
	today        time.Time
	allPlans     map[string][]Plan
	usageMap     map[int]float64
	estimatedMap map[int]bool
	avgUsage     float64
	numPeriods   int
	periodStarts []time.Time
	windowStart  time.Time
	windowEnd    time.Time
}

// usageForPeriod returns (usage kWh, isEstimated) for the given period index.
// Falls back to avgUsage when no historical data exists.
func (pc *projectionContext) usageForPeriod(periodIdx int) (float64, bool) {
	u, ok := pc.usageMap[periodIdx]
	if !ok {
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

// planSearchResult is the output of bestPlanInRange: the cheapest plan found plus the
// date sub-range within [start, end] where that specific plan (by ID) appeared.
type planSearchResult struct {
	plan      *Plan
	dateStart time.Time // earliest fetch_date in [start,end] where plan appeared
	dateEnd   time.Time // latest fetch_date in [start,end] where plan appeared
}

// bestPlanInRange finds the cheapest plan within the inclusive date range [start, end]
// with the given term. Only plans whose TermValue matches termMonths are considered.
// Returns nil if no matching plan is found in the range.
// The returned planSearchResult also carries the date sub-range where that plan appeared.
func (pc *projectionContext) bestPlanInRange(
	termMonths int,
	numCoveredPeriods int, totalUsage float64,
	start, end time.Time,
) *planSearchResult {
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
	if best == nil {
		return nil
	}
	// Second pass: find date sub-range where this exact plan (by ID) appeared in [start, end].
	var dateStart, dateEnd time.Time
	for dateStr, candidates := range pc.allPlans {
		fetchDate, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr != nil || fetchDate.Before(start) || fetchDate.After(end) {
			continue
		}
		for _, r := range candidates {
			if r.ElectricityRateID == best.ElectricityRateID {
				if dateStart.IsZero() || fetchDate.Before(dateStart) {
					dateStart = fetchDate
				}
				if fetchDate.After(dateEnd) {
					dateEnd = fetchDate
				}
				break
			}
		}
	}
	return &planSearchResult{plan: best, dateStart: dateStart, dateEnd: dateEnd}
}

// planSelection is the output of selectBestPlan: the chosen plan plus the window of
// dates during which enrollment should occur to obtain this plan.
type planSelection struct {
	Plan        Plan      // the selected plan (with planKind set)
	ActionStart time.Time // earliest date to enroll (inclusive)
	ActionEnd   time.Time // latest date to enroll (inclusive)
}

// selectBestPlan finds the cheapest plan for decisionDate with the given term.
// termMonths == 1 selects variable plans; termMonths > 1 selects fixed plans.
//
// Phase 1 (within the 30-day enrollment window): searches today's plans via
// bestPlanInRange(today, today). ActionWindow = [today, decisionDate].
//
// Phase 2 (always runs, overrides if cheaper): searches a historical range
// anchored one year before decisionDate. Within the enrollment window the range
// is [today−1yr+1d, decisionDate−1yr]; outside it is [decisionDate−1yr−30d,
// decisionDate−1yr]. The historical date sub-range where the winning plan appeared
// is shifted forward by yearsBack years (clamped to [today, decisionDate]) to
// produce the ActionWindow. Falls back to the most recent available date when no
// data exists in the ideal window.
//
// Returns nil if no data exists from either source.
func (pc *projectionContext) selectBestPlan(termMonths int, decisionDate time.Time) *planSelection {
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
	bestKind := PlanKindProjected
	var bestActionStart, bestActionEnd time.Time

	inEnrollmentWindow := !pc.today.Before(decisionDate.AddDate(0, 0, -30))

	// Phase 1: today's live plans — only within the 30-day enrollment window.
	if inEnrollmentWindow {
		if res := pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, pc.today, pc.today); res != nil {
			cost := float64(numTermPeriods)*res.plan.BaseFee + termUsage*res.plan.PerKwhRate/100.0
			if cost < bestCost {
				bestCost = cost
				bestPlan = res.plan
				bestKind = PlanKindActual
				// Entire enrollment window is open for actual plans.
				bestActionStart = pc.today
				bestActionEnd = decisionDate
			}
		}
	}

	// Phase 2: historical range — always runs, overrides if cheaper.
	// When decisionDate == today the range is inverted (histStart > histEnd) so
	// today's live plans (phase 1) are the sole source.
	//
	// yearsBack: subtract enough years so histEnd lands before today.
	// For decisionDates 2+ years out, -1 year is still in the future.
	yearsBack := 1
	for !decisionDate.AddDate(-yearsBack, 0, 0).Before(pc.today) {
		yearsBack++
	}
	var histStart, histEnd time.Time
	if inEnrollmentWindow {
		histStart = pc.today.AddDate(-yearsBack, 0, 1)
		histEnd = decisionDate.AddDate(-yearsBack, 0, 0)
	} else {
		histStart = decisionDate.AddDate(-yearsBack, 0, -30)
		histEnd = decisionDate.AddDate(-yearsBack, 0, 0)
	}
	if histStart.Before(histEnd) {
		isFallback := false
		histRes := pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, histStart, histEnd)
		if histRes == nil {
			// Fallback: no data in ideal window.
			isFallback = true

			// Special case: if histEnd falls in the May 1 – June 11 gap, use the
			// best plan from June of that same year as the fallback source.
			histEndMD := int(histEnd.Month())*100 + histEnd.Day()
			if histEndMD >= 501 && histEndMD <= 611 {
				juneStart := time.Date(histEnd.Year(), time.June, 1, 0, 0, 0, 0, time.UTC)
				juneEnd := time.Date(histEnd.Year(), time.June, 30, 0, 0, 0, 0, time.UTC)
				histRes = pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, juneStart, juneEnd)
			}

			if histRes == nil {
				// General fallback: use the most recent date that has at least one
				// plan with a matching term.
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
					histRes = pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, latestDate, latestDate)
				}
			}
		}
		if histRes != nil {
			histCost := float64(numTermPeriods)*histRes.plan.BaseFee + termUsage*histRes.plan.PerKwhRate/100.0
			if histCost < bestCost {
				bestCost = histCost
				bestPlan = histRes.plan
				if isFallback {
					bestKind = PlanKindFallback
				} else {
					bestKind = PlanKindProjected
				}
				// Shift the historical date sub-range forward by yearsBack years to
				// produce the action window; clamp to [today, decisionDate].
				aStart := histRes.dateStart.AddDate(yearsBack, 0, 0)
				aEnd := histRes.dateEnd.AddDate(yearsBack, 0, 0)
				if aStart.Before(pc.today) {
					aStart = pc.today
				}
				if aEnd.After(decisionDate) {
					aEnd = decisionDate
				}
				if aStart.After(aEnd) {
					aStart = pc.today
					aEnd = pc.today
				}
				bestActionStart = aStart
				bestActionEnd = aEnd
			}
		}
	}

	if bestPlan == nil {
		return nil
	}

	// Return a copy with planKind stamped in.
	result := *bestPlan
	result.planKind = bestKind
	return &planSelection{Plan: result, ActionStart: bestActionStart, ActionEnd: bestActionEnd}
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
			PeriodCost: round2(cost),
			PlanKind:   segPlan.planKind,
		}
	}
	return breakdown, total
}

// buildGreedyRolling builds plan segments by picking the best term at each decision point.
// termOptions: terms to evaluate (by cost-per-period over the remaining window).
// fallbackTerm: used if no plan is found for any preferred term (0 = no fallback / stop).
// initialETF: charged at the first switch only.
// Returns segments, switches, and the action window (start/end) from the first decision.
func (pc *projectionContext) buildGreedyRolling(termOptions []int, fallbackTerm int, initialETF float64, firstDecisionDate time.Time) ([]planSegment, []SwitchEvent, time.Time, time.Time) {
	var segments []planSegment
	var switches []SwitchEvent
	isFirstIter := true
	var firstActionStart, firstActionEnd time.Time

	decisionDate := pc.windowStart
	for decisionDate.Before(pc.windowEnd) {
		selectDate := decisionDate
		etf := 0.0
		if isFirstIter {
			selectDate = firstDecisionDate
			etf = initialETF
		}

		bestCostPerPeriod := math.MaxFloat64
		var bestSel *planSelection
		bestTermMonths := 0

		for _, termMonths := range termOptions {
			sel := pc.selectBestPlan(termMonths, selectDate)
			if sel == nil {
				continue
			}
			totalCost, periodsCovered := pc.costForDateRange(sel.Plan, decisionDate, pc.windowEnd)
			if periodsCovered <= 0 {
				continue
			}
			costPerPeriod := totalCost / float64(periodsCovered)
			if costPerPeriod < bestCostPerPeriod {
				bestCostPerPeriod = costPerPeriod
				bestSel = sel
				bestTermMonths = termMonths
			}
		}

		// Fallback if no preferred term is available.
		if bestSel == nil && fallbackTerm > 0 {
			bestSel = pc.selectBestPlan(fallbackTerm, selectDate)
			bestTermMonths = fallbackTerm
		}
		if bestSel == nil {
			break
		}

		if isFirstIter {
			firstActionStart = bestSel.ActionStart
			firstActionEnd = bestSel.ActionEnd
			isFirstIter = false
		}

		nextDate := decisionDate.AddDate(0, bestTermMonths, 0)
		segEnd := nextDate
		if segEnd.After(pc.windowEnd) {
			segEnd = pc.windowEnd
		}
		segments = append(segments, planSegment{start: decisionDate, end: segEnd, plan: bestSel.Plan})
		switches = append(switches, SwitchEvent{
			EffectivePeriod: pc.dateToPeriod(decisionDate),
			ETFPaid:         etf,
			Plan:            bestSel.Plan,
		})
		decisionDate = nextDate
	}
	return segments, switches, firstActionStart, firstActionEnd
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

// currentPlanCost computes the cost of staying on the current plan from today until
// the switch date (days away), using day-level precision for the base fee and summing
// usage across offsetMonths full periods.
// Base fee is prorated as: days * baseFee / 30.
// Usage cost is: totalUsageKwh * perKwhCents / 100.
func (pc *projectionContext) currentPlanCost(offsetMonths, days int, baseFee, perKwhCents float64) float64 {
	totalUsage := 0.0
	for i := 0; i < offsetMonths && i < pc.numPeriods; i++ {
		usage, _ := pc.usageForPeriod(i)
		totalUsage += usage
	}
	return float64(days)*baseFee/30.0 + totalUsage*perKwhCents/100.0
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

	// Historical period starts for usage lookup: same T+i offsets, shifted back in
	// time until the date is no longer in the future.  For windows anchored near
	// today this is a simple 1-year lookback; for windows far in the future we keep
	// subtracting years until the historical date has real data available.
	histPeriodStarts := make([]time.Time, numPeriods)
	for i := 0; i < numPeriods; i++ {
		h := windowStart.AddDate(-1, i, 0)
		for h.AddDate(0, 1, 0).After(today) {
			h = h.AddDate(-1, 0, 0)
		}
		histPeriodStarts[i] = h
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
		today:        today,
		allPlans:     allPlans,
		usageMap:     usageMap,
		estimatedMap: estimatedMap,
		avgUsage:     avgUsage,
		numPeriods:   numPeriods,
		periodStarts: periodStarts,
		windowStart:  windowStart,
		windowEnd:    windowEnd,
	}, nil
}

// strategySpec describes one sweep strategy.
type strategySpec struct {
	id, name    string
	termOptions []int
	fallback    int
}

var sweepStrategies = []strategySpec{
	{"variable", "Best variable monthly", []int{1}, 1},
	{"rolling_3m", "3-month rolling", []int{3}, 1},
	{"rolling_6m", "6-month rolling", []int{6}, 1},
	{"fixed_12m", "12-month fixed", []int{12}, 1},
	{"optimal_greedy", "Optimal greedy (3/4/6/12m)", []int{3, 4, 6, 12}, 1},
}

// computeSweep builds a StrategySweep for each strategy type. For each strategy,
// 12 entry dates are evaluated (today + 0..11 months). Each entry captures
// the pre-switch cost (current plan), any applicable ETF, and the 12-month
// post-switch cost anchored at that entry date. The "variable" strategy serves
// as the per-offset baseline for savings comparisons.
func computeSweep(ctx context.Context, pool *pgxpool.Pool, req ProjectionRequest, today time.Time) ([]StrategySweep, error) {
	expiry, err := time.Parse("2006-01-02", req.ContractExpiration)
	if err != nil {
		return nil, fmt.Errorf("invalid contract_expiration: %w", err)
	}
	expiry = time.Date(expiry.Year(), expiry.Month(), expiry.Day(), 0, 0, 0, 0, time.UTC)

	allPlans, err := queryAllDecomposedPlans(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("queryAllDecomposedPlans: %w", err)
	}

	// Build today-anchored context: usage lookups for the pre-switch periods 0..11.
	pcToday, err := newProjectionContext(ctx, pool, today, today, allPlans)
	if err != nil {
		return nil, err
	}

	etfCutoff := expiry.AddDate(0, 0, -14)

	// etfForWindowStart returns the ETF owed if switching at the given date.
	etfForWindowStart := func(windowStart time.Time) float64 {
		if !windowStart.Before(etfCutoff) {
			return 0.0
		}
		if req.ETFPerMonthAmount > 0 {
			return req.ETFPerMonthAmount * float64(monthsBetween(windowStart, expiry))
		}
		return req.ETFAmount
	}

	const numOffsets = 26 // bi-weekly steps: 26 × 14 days ≈ 12 months
	sweeps := make([]StrategySweep, len(sweepStrategies))

	for si, spec := range sweepStrategies {
		sweep := StrategySweep{
			StrategyID:   spec.id,
			StrategyName: spec.name,
			Entries:      make([]SweepEntry, numOffsets),
		}

		for offset := 0; offset < numOffsets; offset++ {
			windowStart := today.AddDate(0, 0, offset*14) // advance by 14 days per step
			etf := etfForWindowStart(windowStart)
			days := int(windowStart.Sub(today).Hours() / 24)
			preCost := round2(pcToday.currentPlanCost(offset, days, req.CurrentPlanBaseFee, req.CurrentPlanCents))

			pc, err := newProjectionContext(ctx, pool, today, windowStart, allPlans)
			if err != nil {
				return nil, err
			}

			segments, switches, actionStart, actionEnd := pc.buildGreedyRolling(spec.termOptions, spec.fallback, etf, windowStart)
			breakdown, postCost := pc.buildBreakdown(segments)
			if switches == nil {
				switches = []SwitchEvent{}
			}

			actionDateStart := ""
			actionDateEnd := ""
			if !actionStart.IsZero() {
				actionDateStart = actionStart.Format("2006-01-02")
			}
			if !actionEnd.IsZero() {
				actionDateEnd = actionEnd.Format("2006-01-02")
			}

			sweep.Entries[offset] = SweepEntry{
				WindowStart:     windowStart.Format("2006-01-02"),
				WeeksFromToday:  offset * 2,
				PreSwitchCost:   preCost,
				ETFApplied:      round2(etf),
				PostSwitchCost:  round2(postCost),
				TotalCost:       round2(preCost + etf + postCost),
				PeriodBreakdown: breakdown,
				Switches:        switches,
				SwitchCount:     len(switches),
				ActionDateStart: actionDateStart,
				ActionDateEnd:   actionDateEnd,
			}
		}

		// Best entry = lowest TotalCost.
		bestIdx := 0
		for i := 1; i < numOffsets; i++ {
			if sweep.Entries[i].TotalCost < sweep.Entries[bestIdx].TotalCost {
				bestIdx = i
			}
		}
		sweep.BestEntryIndex = bestIdx

		// Best entry (post-switch only) = lowest PostSwitchCost.
		bestIdxPost := 0
		for i := 1; i < numOffsets; i++ {
			if sweep.Entries[i].PostSwitchCost < sweep.Entries[bestIdxPost].PostSwitchCost {
				bestIdxPost = i
			}
		}
		sweep.BestEntryIndexPostSwitch = bestIdxPost
		sweeps[si] = sweep
	}

	// Savings vs variable baseline (index 0) at the same offset.
	for si := range sweeps {
		for offset := 0; offset < numOffsets; offset++ {
			savings := round2(sweeps[0].Entries[offset].TotalCost - sweeps[si].Entries[offset].TotalCost)
			sweeps[si].Entries[offset].SavingsVsBaseline = savings
		}
	}

	return sweeps, nil
}
