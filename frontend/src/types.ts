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

export interface UsageMonth {
  month: string
  total_kwh: number
}
