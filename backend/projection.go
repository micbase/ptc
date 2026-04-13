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
	bestKind := PlanKindProjected

	inEnrollmentWindow := !pc.today.Before(decisionDate.AddDate(0, 0, -30))

	// Phase 1: today's live plans — only within the 30-day enrollment window.
	if inEnrollmentWindow {
		if p := pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, pc.today, pc.today); p != nil {
			cost := float64(numTermPeriods)*p.BaseFee + termUsage*p.PerKwhRate/100.0
			if cost < bestCost {
				bestCost = cost
				bestPlan = p
				bestKind = PlanKindActual
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
		histPlan := pc.bestPlanInRange(termMonths, numTermPeriods, termUsage, histStart, histEnd)
		if histPlan == nil {
			// Fallback: no data in ideal window — use the most recent date that has
			// at least one plan with a matching term.
			isFallback = true
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
				if isFallback {
					bestKind = PlanKindFallback
				} else {
					bestKind = PlanKindProjected
				}
			}
		}
	}

	if bestPlan == nil {
		return nil
	}

	// Return a copy with planKind stamped in.
	result := *bestPlan
	result.planKind = bestKind
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
func (pc *projectionContext) buildGreedyRolling(termOptions []int, fallbackTerm int, initialETF float64, firstDecisionDate time.Time) ([]planSegment, []SwitchEvent) {
	var segments []planSegment
	var switches []SwitchEvent
	isFirst := true

	decisionDate := pc.windowStart
	for decisionDate.Before(pc.windowEnd) {
		selectDate := decisionDate
		etf := 0.0
		if isFirst {
			selectDate = firstDecisionDate
			etf = initialETF
			isFirst = false
		}

		bestCostPerPeriod := math.MaxFloat64
		var bestPlanRes *Plan
		bestTermMonths := 0

		for _, termMonths := range termOptions {
			planRes := pc.selectBestPlan(termMonths, selectDate)
			if planRes == nil {
				continue
			}
			totalCost, periodsCovered := pc.costForDateRange(*planRes, decisionDate, pc.windowEnd)
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

		// Fallback if no preferred term is available.
		if bestPlanRes == nil && fallbackTerm > 0 {
			bestPlanRes = pc.selectBestPlan(fallbackTerm, selectDate)
			bestTermMonths = fallbackTerm
		}
		if bestPlanRes == nil {
			break
		}

		nextDate := decisionDate.AddDate(0, bestTermMonths, 0)
		segEnd := nextDate
		if segEnd.After(pc.windowEnd) {
			segEnd = pc.windowEnd
		}
		segments = append(segments, planSegment{start: decisionDate, end: segEnd, plan: *bestPlanRes})
		switches = append(switches, SwitchEvent{
			EffectivePeriod: pc.dateToPeriod(decisionDate),
			ETFPaid:         etf,
			Plan:            *bestPlanRes,
		})
		decisionDate = nextDate
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

			segments, switches := pc.buildGreedyRolling(spec.termOptions, spec.fallback, etf, windowStart)
			breakdown, postCost := pc.buildBreakdown(segments)
			if switches == nil {
				switches = []SwitchEvent{}
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
