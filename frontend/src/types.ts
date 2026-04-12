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
  contract_expiration: string
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
  enroll_url: string
  fetch_date: string
}

export interface AddSwitchEventRequest {
  electricity_rate_id: number
  switch_date: string
  contract_expiration_date: string
  notes: string
}

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
  confidence: string
  is_projected: boolean  // true when rates are a historical estimate, not today's live rates
}

export interface StrategyResult {
  strategy_id: string
  strategy_name: string
  total_cost: number
  total_savings_vs_baseline: number
  etf_paid: number
  net_savings: number
  switch_count: number
  confidence: string
  switches: SwitchEvent[]
  period_breakdown: PeriodBreakdown[]
}
