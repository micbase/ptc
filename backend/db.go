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

func queryUsageMonthly(ctx context.Context, pool *pgxpool.Pool) ([]UsageMonth, error) {
	rows, err := pool.Query(ctx, `
		SELECT date_trunc('month', interval_start)::date::text AS month,
		       ROUND(SUM(consumption_kwh)::numeric, 2)::float8 AS total_kwh
		FROM usage_intervals
		GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	months := make([]UsageMonth, 0)
	for rows.Next() {
		var m UsageMonth
		if err := rows.Scan(&m.Month, &m.TotalKwh); err != nil {
			return nil, err
		}
		months = append(months, m)
	}
	return months, rows.Err()
}

func queryUsageAvg(ctx context.Context, pool *pgxpool.Pool) (float64, error) {
	var avg float64
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(ROUND(AVG(total_kwh)::numeric, 2)::float8, 0)
		FROM (
			SELECT date_trunc('month', interval_start) AS month,
			       SUM(consumption_kwh) AS total_kwh
			FROM usage_intervals
			WHERE date_trunc('month', interval_start) < date_trunc('month', CURRENT_DATE)
			  AND date_trunc('month', interval_start) >= date_trunc('month', CURRENT_DATE) - INTERVAL '12 months'
			GROUP BY 1
		) sub`).Scan(&avg)
	if err != nil {
		return 0, err
	}
	return avg, nil
}

func queryLatestDate(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var d time.Time
	err := pool.QueryRow(ctx, `SELECT max(fetch_date) FROM electricity_rates`).Scan(&d)
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}
