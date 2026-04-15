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

type SwitchRecord struct {
	ID                     int     `json:"id"`
	ElectricityRateID      int     `json:"electricity_rate_id"`
	SwitchDate             string  `json:"switch_date"`
	ContractExpirationDate string  `json:"contract_expiration_date"`
	Notes                  string  `json:"notes"`
	CreatedAt              string  `json:"created_at"`
	// Joined from electricity_rates
	RepCompany string  `json:"rep_company"`
	Product    string  `json:"product"`
	TermValue  int     `json:"term_value"`
	RateType   string  `json:"rate_type"`
	Kwh1000    float64 `json:"kwh1000"`
	CancelFee  string  `json:"cancel_fee"`
	FetchDate  string  `json:"fetch_date"`
	// Decomposed rates (computed from kwh500/1000/2000)
	BaseFee    float64 `json:"base_fee"`     // $ per month
	PerKwhRate float64 `json:"per_kwh_rate"` // ¢/kWh (marginal)
}

type AddSwitchEventRequest struct {
	ElectricityRateID      int    `json:"electricity_rate_id"`
	SwitchDate             string `json:"switch_date"`
	ContractExpirationDate string `json:"contract_expiration_date"`
	Notes                  string `json:"notes"`
}

// ProjectionRequest specifies ETF terms, contract expiration, and current plan rates.
// CurrentPlanCents and CurrentPlanBaseFee are the decomposed marginal rate (¢/kWh)
// and base fee ($/month) of the user's current plan, used to price the pre-switch period.
type ProjectionRequest struct {
	ETFAmount          float64 `json:"etf_amount"`
	ETFPerMonthAmount  float64 `json:"etf_per_month_amount"`
	ContractExpiration string  `json:"contract_expiration"`
	CurrentPlanCents   float64 `json:"current_plan_cents"`    // ¢/kWh (decomposed marginal rate)
	CurrentPlanBaseFee float64 `json:"current_plan_base_fee"` // $ per month
}

// SweepEntry represents one candidate entry date within a strategy sweep.
type SweepEntry struct {
	WindowStart       string            `json:"window_start"`        // "YYYY-MM-DD"
	WeeksFromToday    int               `json:"weeks_from_today"`    // 0, 2, 4, ... 50 (bi-weekly steps)
	PreSwitchCost     float64           `json:"pre_switch_cost"`     // current plan cost today → windowStart
	ETFApplied        float64           `json:"etf_applied"`         // ETF owed if switching at windowStart
	PostSwitchCost    float64           `json:"post_switch_cost"`    // 12-month strategy cost from windowStart
	TotalCost         float64           `json:"total_cost"`          // pre + ETF + post
	SavingsVsBaseline float64           `json:"savings_vs_baseline"` // vs variable baseline at same offset
	PeriodBreakdown   []PeriodBreakdown `json:"period_breakdown"`    // 12 periods anchored at windowStart
	Switches          []SwitchEvent     `json:"switches"`
	SwitchCount       int               `json:"switch_count"`
	ActionDateStart   string            `json:"action_date_start"` // earliest date to enroll for first plan (YYYY-MM-DD)
	ActionDateEnd     string            `json:"action_date_end"`   // latest date to enroll for first plan (YYYY-MM-DD)
}

// StrategySweep holds all 26 bi-weekly entry-date options for one strategy type.
type StrategySweep struct {
	StrategyID                string       `json:"strategy_id"`
	StrategyName              string       `json:"strategy_name"`
	Entries                   []SweepEntry `json:"entries"`                      // indices 0..25 (bi-weekly steps from today)
	BestEntryIndex            int          `json:"best_entry_index"`             // index with lowest TotalCost
	BestEntryIndexPostSwitch  int          `json:"best_entry_index_post_switch"` // index with lowest PostSwitchCost
}
