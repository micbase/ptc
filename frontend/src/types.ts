export interface ElectricityRate {
  id: number
  fetch_date: string
  id_key: string
  tdu_company_name: string
  rep_company: string
  product: string
  kwh500: number
  kwh1000: number
  kwh2000: number
  fees_credits: string
  prepaid: boolean
  time_of_use: boolean
  fixed: number // INTEGER in DB
  rate_type: string
  renewable: number // INTEGER (percentage)
  term_value: number
  cancel_fee: string
  website: string
  special_terms: string
  terms_url: string
  yrac_url: string
  promotion: boolean
  promotion_desc: string
  facts_url: string
  enroll_url: string
  prepaid_url: string
  enroll_phone: string
  new_customer: boolean
  min_usage_fees_credits: boolean
  language: string
  rating: number // INTEGER
  processed_at: string
}

export interface ChartPoint {
  fetch_date: string
  kwh1000: number
}

export interface ProjectionRequest {
  etf_amount: number
  etf_per_month_amount: number
  contract_expiration: string
  current_plan_cents: number    // ¢/kWh (decomposed marginal rate)
  current_plan_base_fee: number // $ per month
}

export interface Plan {
  electricity_rate_id: number // electricity_rates.id, for recording switch events
  rep_company: string
  product: string
  term_value: number
  rate_type: string
  base_fee: number      // $ per month (decomposed)
  per_kwh_rate: number  // ¢/kWh (decomposed)
  enroll_url: string
  kwh1000_cents: number // ¢/kWh all-in at 1000 kWh
}

export interface SwitchEvent {
  effective_period: string
  etf_paid: number
  plan: Plan
}

// A recorded switch event stored in the DB switch_events table.
export interface SwitchRecord {
  id: number
  electricity_rate_id: number
  switch_date: string
  contract_expiration_date: string
  notes: string
  created_at: string
  // Joined from electricity_rates
  rep_company: string
  product: string
  term_value: number
  rate_type: string
  kwh1000: number
  cancel_fee: string
  fetch_date: string
  // Decomposed rates
  base_fee: number     // $ per month
  per_kwh_rate: number // ¢/kWh (marginal)
  // Computed from usage_intervals for the period this plan was active
  total_usage_kwh: number // kWh consumed during active period
  total_cost: number      // $ total cost (base fees + usage)
  period_days: number     // number of days the plan was active
}

export interface AddSwitchEventRequest {
  electricity_rate_id: number
  switch_date: string
  contract_expiration_date: string
  notes: string
}

// PlanKind describes the source of a period's rates.
// actual    = today's live market rates (enrollment is available).
// projected = historical rates from ~1 year ago used as a proxy for a future period.
// fallback  = most-recent available historical rates (ideal window had no data).
export type PlanKind = 'actual' | 'projected' | 'fallback'

export interface PeriodBreakdown {
  period: string        // "T+N" period label
  period_start: string  // "YYYY-MM-DD"
  period_end: string    // "YYYY-MM-DD" (inclusive last day)
  usage_kwh: number
  usage_is_estimated: boolean
  active_plan: Plan
  rate_cents: number
  base_fee: number
  period_cost: number
  plan_kind: PlanKind   // "actual" | "projected" | "fallback"
}

// One candidate entry date within a strategy sweep.
export interface SweepEntry {
  window_start: string        // "YYYY-MM-DD"
  weeks_from_today: number    // 0, 2, 4, ... 50 (bi-weekly steps)
  pre_switch_cost: number     // current plan cost today → window_start
  etf_applied: number         // ETF owed if switching at window_start
  post_switch_cost: number    // 12-month strategy cost from window_start
  total_cost: number          // pre + ETF + post
  savings_vs_baseline: number // vs variable baseline at same offset
  period_breakdown: PeriodBreakdown[]
  switches: SwitchEvent[]
  switch_count: number
  action_date_start: string   // earliest date to enroll for first plan ("YYYY-MM-DD")
  action_date_end: string     // latest date to enroll for first plan ("YYYY-MM-DD")
}

// Sweep over 26 bi-weekly entry-date options for one strategy type.
export interface StrategySweep {
  strategy_id: string
  strategy_name: string
  entries: SweepEntry[]                  // indices 0..25 (bi-weekly steps from today)
  best_entry_index: number               // index with lowest total_cost
  best_entry_index_post_switch: number   // index with lowest post_switch_cost
}
