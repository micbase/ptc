package main

type ElectricityRate struct {
	ID                  int      `json:"id"`
	FetchDate           string   `json:"fetch_date"`
	IDKey               *string  `json:"id_key"`
	TDUCompanyName      *string  `json:"tdu_company_name"`
	RepCompany          *string  `json:"rep_company"`
	Product             *string  `json:"product"`
	Kwh500              *float64 `json:"kwh500"`
	Kwh1000             *float64 `json:"kwh1000"`
	Kwh2000             *float64 `json:"kwh2000"`
	FeesCredits         *string  `json:"fees_credits"`
	Prepaid             *bool    `json:"prepaid"`
	TimeOfUse           *bool    `json:"time_of_use"`
	Fixed               *int     `json:"fixed"`
	RateType            *string  `json:"rate_type"`
	Renewable           *int     `json:"renewable"`
	TermValue           *int     `json:"term_value"`
	CancelFee           *string  `json:"cancel_fee"`
	Website             *string  `json:"website"`
	SpecialTerms        *string  `json:"special_terms"`
	TermsURL            *string  `json:"terms_url"`
	YracURL             *string  `json:"yrac_url"`
	Promotion           *bool    `json:"promotion"`
	PromotionDesc       *string  `json:"promotion_desc"`
	FactsURL            *string  `json:"facts_url"`
	EnrollURL           *string  `json:"enroll_url"`
	PrepaidURL          *string  `json:"prepaid_url"`
	EnrollPhone         *string  `json:"enroll_phone"`
	NewCustomer         *bool    `json:"new_customer"`
	MinUsageFeesCredits *bool    `json:"min_usage_fees_credits"`
	Language            *string  `json:"language"`
	Rating              *int     `json:"rating"`
	ProcessedAt         *string  `json:"processed_at"`
}

type ChartPoint struct {
	FetchDate string  `json:"fetch_date"`
	Kwh1000   float64 `json:"kwh1000"`
}

type UsageMonth struct {
	Month    string  `json:"month"`
	TotalKwh float64 `json:"total_kwh"`
}
