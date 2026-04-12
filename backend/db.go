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

// queryAllDecomposedPlans returns all ONCOR plans for every fetch_date that pass the
// 3-point linearity check, keyed by "YYYY-MM-DD". Each Plan carries the full
// plan metadata (company, product, term, enroll URL, etc.) alongside the decomposed
// base_fee ($) and per_kwh_rate (¢/kWh). Today's plans are included under their date
// key, so callers can extract them with allPlans[today].
func queryAllDecomposedPlans(ctx context.Context, pool *pgxpool.Pool) (map[string][]Plan, error) {
	query := `
		SELECT
			id,
			fetch_date::text,
			COALESCE(rep_company, ''),
			COALESCE(product, ''),
			COALESCE(rate_type, ''),
			COALESCE(term_value, 0),
			kwh500::float8,
			kwh1000::float8,
			kwh2000::float8,
			COALESCE(enroll_url, '')
		FROM electricity_rates
		WHERE tdu_company_name ILIKE '%ONCOR%'
		  AND min_usage_fees_credits = false
		  AND time_of_use = false
		  AND language = 'English'
		  AND kwh500 IS NOT NULL
		  AND kwh1000 IS NOT NULL
		  AND kwh2000 IS NOT NULL`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]Plan)
	for rows.Next() {
		var (
			rateID                                            int
			dateStr, repCompany, product, rateType, enrollURL string
			termValue                                         int
			kwh500, kwh1000, kwh2000                         float64
		)
		if err := rows.Scan(&rateID, &dateStr, &repCompany, &product, &rateType, &termValue,
			&kwh500, &kwh1000, &kwh2000, &enrollURL); err != nil {
			return nil, err
		}

		// DB columns (kwh500/1000/2000) are average $/kWh at that usage tier.
		// Compute marginal rate in $/kWh, then convert to ¢/kWh.
		// 3-point linearity check
		rateABdol := (1000*kwh1000 - 500*kwh500) / 500   // $/kWh, 500→1000
		rateBCdol := (2000*kwh2000 - 1000*kwh1000) / 1000 // $/kWh, 1000→2000
		if absf(rateABdol) < 1e-9 {
			continue
		}
		if absf(rateABdol-rateBCdol)/absf(rateABdol) > 0.15 {
			continue
		}
		// base_fee ($) = total_cost_at_500 - 500 * marginal_rate
		baseFee := 500*kwh500 - 500*rateABdol
		if baseFee < 0 {
			continue
		}
		result[dateStr] = append(result[dateStr], Plan{
			ElectricityRateID: rateID,
			RepCompany:        repCompany,
			Product:           product,
			TermValue:         termValue,
			RateType:          rateType,
			BaseFee:           baseFee,         // $
			PerKwhRate:        rateABdol * 100, // ¢/kWh
			EnrollURL:         enrollURL,
			Kwh1000Cents:      kwh1000 * 100, // ¢/kWh all-in at 1000 kWh
		})
	}
	return result, rows.Err()
}

// decomposeRate computes base_fee ($) and per_kwh_rate (¢/kWh) from the three-point
// electricity rate data. Returns (0, 0) if the data fails the linearity check.
func decomposeRate(kwh500, kwh1000, kwh2000 float64) (baseFee, perKwhRate float64) {
	rateABdol := (1000*kwh1000 - 500*kwh500) / 500
	if absf(rateABdol) < 1e-9 {
		return 0, 0
	}
	rateBCdol := (2000*kwh2000 - 1000*kwh1000) / 1000
	if absf(rateABdol-rateBCdol)/absf(rateABdol) > 0.15 {
		return 0, 0
	}
	bf := 500*kwh500 - 500*rateABdol
	if bf < 0 {
		return 0, 0
	}
	return bf, rateABdol * 100
}

// querySwitchEvents returns switch events ordered by switch_date DESC.
// Pass limit=1 to get only the most recent; limit=0 returns all.
func querySwitchEvents(ctx context.Context, pool *pgxpool.Pool, limit int) ([]SwitchRecord, error) {
	q := `SELECT ` + switchEventSelectCols + `
		FROM switch_events se
		JOIN electricity_rates er ON er.id = se.electricity_rate_id
		ORDER BY se.switch_date DESC, se.created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]SwitchRecord, 0)
	for rows.Next() {
		r, err := scanSwitchRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

const switchEventSelectCols = `
		se.id,
		se.electricity_rate_id,
		se.switch_date::text,
		se.contract_expiration_date::text,
		COALESCE(se.notes, ''),
		se.created_at::text,
		COALESCE(er.rep_company, ''),
		COALESCE(er.product, ''),
		COALESCE(er.term_value, 0),
		COALESCE(er.rate_type, ''),
		COALESCE(er.kwh1000::float8, 0),
		COALESCE(er.cancel_fee, ''),
		er.fetch_date::text,
		COALESCE(er.kwh500::float8, 0),
		COALESCE(er.kwh2000::float8, 0)`

func scanSwitchRecord(row interface {
	Scan(dest ...any) error
}) (SwitchRecord, error) {
	var r SwitchRecord
	var kwh500, kwh2000 float64
	if err := row.Scan(
		&r.ID, &r.ElectricityRateID, &r.SwitchDate, &r.ContractExpirationDate,
		&r.Notes, &r.CreatedAt, &r.RepCompany, &r.Product, &r.TermValue,
		&r.RateType, &r.Kwh1000, &r.CancelFee, &r.FetchDate,
		&kwh500, &kwh2000,
	); err != nil {
		return SwitchRecord{}, err
	}
	r.BaseFee, r.PerKwhRate = decomposeRate(kwh500, r.Kwh1000, kwh2000)
	return r, nil
}

func insertSwitchEvent(ctx context.Context, pool *pgxpool.Pool, req AddSwitchEventRequest) (SwitchRecord, error) {
	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO switch_events (electricity_rate_id, switch_date, contract_expiration_date, notes)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		req.ElectricityRateID, req.SwitchDate, req.ContractExpirationDate, req.Notes,
	).Scan(&id)
	if err != nil {
		return SwitchRecord{}, err
	}

	row := pool.QueryRow(ctx, `
		SELECT `+switchEventSelectCols+`
		FROM switch_events se
		JOIN electricity_rates er ON er.id = se.electricity_rate_id
		WHERE se.id = $1`, id)
	return scanSwitchRecord(row)
}


func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// queryPeriodUsage returns projected usage for the 12 T+x periods by aggregating
// usage_intervals from the historical window (1 year prior to each period).
// histPeriodStarts[i] is the start of the historical period corresponding to T+i
// (i.e. windowStart - 1 year + i months). Returns usage keyed by period index (0–11)
// and whether the majority of readings in that period are estimated.
func queryPeriodUsage(ctx context.Context, pool *pgxpool.Pool, histPeriodStarts []time.Time) (map[int]float64, map[int]bool, error) {
	if len(histPeriodStarts) == 0 {
		return map[int]float64{}, map[int]bool{}, nil
	}
	overallStart := histPeriodStarts[0]
	overallEnd := histPeriodStarts[len(histPeriodStarts)-1].AddDate(0, 1, 0)

	query := `
		SELECT interval_start, consumption_kwh::float8, is_actual
		FROM usage_intervals
		WHERE interval_start >= $1 AND interval_start < $2`

	rows, err := pool.Query(ctx, query, overallStart, overallEnd)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	usageMap := make(map[int]float64)
	totalCount := make(map[int]int)
	estimatedCount := make(map[int]int)

	for rows.Next() {
		var ts time.Time
		var kwh float64
		var isActual bool
		if err := rows.Scan(&ts, &kwh, &isActual); err != nil {
			return nil, nil, err
		}
		// Find which historical period this interval belongs to.
		for i, ps := range histPeriodStarts {
			pe := ps.AddDate(0, 1, 0)
			if !ts.Before(ps) && ts.Before(pe) {
				usageMap[i] += kwh
				totalCount[i]++
				if !isActual {
					estimatedCount[i]++
				}
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	estimatedMap := make(map[int]bool)
	for i, cnt := range totalCount {
		estimatedMap[i] = cnt > 0 && estimatedCount[i] > cnt/2
	}
	return usageMap, estimatedMap, nil
}

