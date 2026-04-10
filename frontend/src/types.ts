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
  current_rate_cents: number
  current_base_fee: number
  etf_amount: number
  contract_expiration: string
}

export interface ProjectionPlanInfo {
  id_key: string
  rep_company: string
  product: string
  term_value: number
  rate_type: string
  projected_rate_cents: number
  projected_base_fee: number
  renewable: number
  rating: number
  enroll_url: string
}

export interface SwitchEvent {
  effective_month: string
  etf_paid: number
  plan: ProjectionPlanInfo
}

export interface MonthlyBreakdown {
  month: string
  usage_kwh: number
  usage_is_estimated: boolean
  active_plan_label: string
  rate_cents: number
  base_fee: number
  monthly_cost: number
  confidence: string
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
  monthly_breakdown: MonthlyBreakdown[]
}
