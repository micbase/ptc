package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const baseWhere = `
	tdu_company_name = 'ONCOR ELECTRIC DELIVERY COMPANY'
	AND min_usage_fees_credits = false
	AND time_of_use = false
	AND language = 'English'`

func queryPlans(ctx context.Context, pool *pgxpool.Pool, date string) ([]ElectricityRate, error) {
	query := fmt.Sprintf(`
		SELECT id, fetch_date::text, id_key, tdu_company_name, rep_company, product,
			kwh500::float8, kwh1000::float8, kwh2000::float8,
			fees_credits, prepaid, time_of_use,
			fixed, rate_type, renewable, term_value, cancel_fee, website,
			special_terms, terms_url, yrac_url, promotion, promotion_desc,
			facts_url, enroll_url, prepaid_url, enroll_phone, new_customer,
			min_usage_fees_credits, language, rating, processed_at::text
		FROM electricity_rates
		WHERE %s AND fetch_date = $1
		ORDER BY kwh1000 ASC`, baseWhere)

	rows, err := pool.Query(ctx, query, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	plans := make([]ElectricityRate, 0)
	for rows.Next() {
		var r ElectricityRate
		err := rows.Scan(
			&r.ID, &r.FetchDate, &r.IDKey, &r.TDUCompanyName, &r.RepCompany, &r.Product,
			&r.Kwh500, &r.Kwh1000, &r.Kwh2000, &r.FeesCredits, &r.Prepaid, &r.TimeOfUse,
			&r.Fixed, &r.RateType, &r.Renewable, &r.TermValue, &r.CancelFee, &r.Website,
			&r.SpecialTerms, &r.TermsURL, &r.YracURL, &r.Promotion, &r.PromotionDesc,
			&r.FactsURL, &r.EnrollURL, &r.PrepaidURL, &r.EnrollPhone, &r.NewCustomer,
			&r.MinUsageFeesCredits, &r.Language, &r.Rating, &r.ProcessedAt,
		)
		if err != nil {
			return nil, err
		}
		plans = append(plans, r)
	}
	return plans, rows.Err()
}

func queryChart(ctx context.Context, pool *pgxpool.Pool, chartType string) ([]ChartPoint, error) {
	var extra string
	switch chartType {
	case "best_3m":
		extra = " AND term_value = 3"
	case "variable":
		extra = " AND rate_type = 'Variable'"
	default:
		extra = ""
	}

	query := fmt.Sprintf(`
		SELECT fetch_date::text, min(kwh1000)::float8 AS kwh1000
		FROM electricity_rates
		WHERE %s%s
		GROUP BY fetch_date
		ORDER BY fetch_date`, baseWhere, extra)

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ChartPoint, error) {
		var p ChartPoint
		err := row.Scan(&p.FetchDate, &p.Kwh1000)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	return points, nil
}

func queryLatestDate(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var d time.Time
	err := pool.QueryRow(ctx, `SELECT max(fetch_date) FROM electricity_rates`).Scan(&d)
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

// queryLinearPlans returns all ONCOR plans for today that pass the 3-point linearity check,
// with decomposed base_fee ($) and per_kwh_rate (¢/kWh).
func queryLinearPlans(ctx context.Context, pool *pgxpool.Pool, today time.Time) ([]LinearPlan, error) {
	query := `
		SELECT
			COALESCE(id_key, ''),
			COALESCE(rep_company, ''),
			COALESCE(product, ''),
			COALESCE(rate_type, ''),
			COALESCE(term_value, 0),
			kwh500::float8,
			kwh1000::float8,
			kwh2000::float8,
			COALESCE(renewable, 0),
			COALESCE(rating::float8, 0),
			COALESCE(enroll_url, '')
		FROM electricity_rates
		WHERE tdu_company_name ILIKE '%ONCOR%'
		  AND min_usage_fees_credits = false
		  AND time_of_use = false
		  AND language = 'English'
		  AND fetch_date = $1
		  AND kwh500 IS NOT NULL
		  AND kwh1000 IS NOT NULL
		  AND kwh2000 IS NOT NULL`

	rows, err := pool.Query(ctx, query, today.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []LinearPlan
	for rows.Next() {
		var (
			idKey, repCompany, product, rateType, enrollURL string
			termValue, renewable                            int
			rating, kwh500, kwh1000, kwh2000                float64
		)
		if err := rows.Scan(&idKey, &repCompany, &product, &rateType, &termValue,
			&kwh500, &kwh1000, &kwh2000, &renewable, &rating, &enrollURL); err != nil {
			return nil, err
		}

		// 3-point linearity check
		// rate_AB = marginal ¢/kWh between 500 and 1000 kWh
		// rate_BC = marginal ¢/kWh between 1000 and 2000 kWh
		rateAB := (1000*kwh1000 - 500*kwh500) / 500
		rateBC := (2000*kwh2000 - 1000*kwh1000) / 1000
		if rateAB == 0 {
			continue
		}
		if absf(rateAB-rateBC)/absf(rateAB) > 0.15 {
			continue
		}
		// base_fee in cents = total_cost_500 - 500*rateAB
		baseFeeCents := 500*kwh500 - 500*rateAB
		if baseFeeCents < 0 {
			continue
		}
		plans = append(plans, LinearPlan{
			IDKey:      idKey,
			RepCompany: repCompany,
			Product:    product,
			TermValue:  termValue,
			RateType:   rateType,
			BaseFee:    baseFeeCents / 100, // convert ¢ to $
			PerKwhRate: rateAB,
			Renewable:  renewable,
			Rating:     rating,
			EnrollURL:  enrollURL,
		})
	}
	return plans, rows.Err()
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// queryMonthlyUsage returns projected usage for the window by aggregating
// usage_intervals from [histStart, histEnd) and mapping each hist month forward 1 year.
// Returns usage keyed by "YYYY-MM" (future month) and a map of whether that month
// has a majority of estimated readings.
func queryMonthlyUsage(ctx context.Context, pool *pgxpool.Pool, histStart, histEnd time.Time) (map[string]float64, map[string]bool, error) {
	start := histStart
	end := histEnd

	query := `
		SELECT
			to_char(interval_start, 'YYYY-MM') AS hist_month,
			SUM(consumption_kwh)::float8 AS usage_kwh,
			CASE WHEN COUNT(*) > 0
				THEN COUNT(*) FILTER (WHERE is_actual = false)::float8 / COUNT(*)::float8
				ELSE 0
			END AS estimated_ratio
		FROM usage_intervals
		WHERE interval_start >= $1 AND interval_start < $2
		GROUP BY to_char(interval_start, 'YYYY-MM')`

	rows, err := pool.Query(ctx, query, start, end)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	usageMap := make(map[string]float64)
	estimatedMap := make(map[string]bool)
	for rows.Next() {
		var histMonth string
		var usageKwh, estimatedRatio float64
		if err := rows.Scan(&histMonth, &usageKwh, &estimatedRatio); err != nil {
			return nil, nil, err
		}
		// Map "2025-04" → "2026-04" (add 1 year)
		t, err := time.Parse("2006-01", histMonth)
		if err != nil {
			continue
		}
		futureMonth := t.AddDate(1, 0, 0).Format("2006-01")
		usageMap[futureMonth] = usageKwh
		estimatedMap[futureMonth] = estimatedRatio > 0.5
	}
	return usageMap, estimatedMap, rows.Err()
}

// queryHistoricalMinRates returns the minimum kwh1000 rate by calendar month.
// isVariable=true queries variable plans; false queries fixed plans.
func queryHistoricalMinRates(ctx context.Context, pool *pgxpool.Pool, isVariable bool) (map[string]float64, error) {
	rateFilter := " AND rate_type != 'Variable'"
	if isVariable {
		rateFilter = " AND rate_type = 'Variable'"
	}
	query := fmt.Sprintf(`
		SELECT to_char(fetch_date, 'YYYY-MM') AS month, MIN(kwh1000)::float8 AS min_rate
		FROM electricity_rates
		WHERE tdu_company_name ILIKE '%%ONCOR%%'
		  AND min_usage_fees_credits = false
		  AND time_of_use = false
		  AND language = 'English'
		  AND kwh1000 IS NOT NULL
		  %s
		GROUP BY to_char(fetch_date, 'YYYY-MM')`, rateFilter)

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]float64)
	for rows.Next() {
		var month string
		var minRate float64
		if err := rows.Scan(&month, &minRate); err != nil {
			return nil, err
		}
		result[month] = minRate
	}
	return result, rows.Err()
}
