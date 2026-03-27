package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func upsertIntervals(ctx context.Context, pool *pgxpool.Pool, intervals []SMTInterval) (int, error) {
	if len(intervals) == 0 {
		return 0, nil
	}

	const chunkSize = 500
	total := 0
	for i := 0; i < len(intervals); i += chunkSize {
		end := i + chunkSize
		if end > len(intervals) {
			end = len(intervals)
		}
		chunk := intervals[i:end]

		placeholders := make([]string, len(chunk))
		args := make([]interface{}, 0, len(chunk)*3)
		for j, iv := range chunk {
			base := j * 3
			placeholders[j] = fmt.Sprintf("($%d,$%d,$%d)", base+1, base+2, base+3)
			args = append(args, iv.Start, iv.ConsumptionKwh, iv.IsActual)
		}

		q := fmt.Sprintf(`
			INSERT INTO usage_intervals (interval_start, consumption_kwh, is_actual)
			VALUES %s
			ON CONFLICT (interval_start) DO UPDATE
			  SET consumption_kwh = EXCLUDED.consumption_kwh,
			      is_actual       = EXCLUDED.is_actual`,
			strings.Join(placeholders, ","))

		ct, err := pool.Exec(ctx, q, args...)
		if err != nil {
			return total, fmt.Errorf("upsertIntervals: %w", err)
		}
		total += int(ct.RowsAffected())
	}
	return total, nil
}

// SMTCoverage describes the date range we have in usage_intervals.
type SMTCoverage struct {
	OldestDate *string `json:"oldest_date"` // nil if no data
	NewestDate *string `json:"newest_date"`
	TotalRows  int     `json:"total_rows"`
}

func queryUsageCoverage(ctx context.Context, pool *pgxpool.Pool) (*SMTCoverage, error) {
	var cov SMTCoverage
	var oldest, newest *time.Time
	err := pool.QueryRow(ctx, `
		SELECT MIN(interval_start), MAX(interval_start), COUNT(*)
		FROM usage_intervals`).
		Scan(&oldest, &newest, &cov.TotalRows)
	if err != nil {
		return nil, err
	}
	if oldest != nil {
		s := oldest.Format("2006-01-02")
		cov.OldestDate = &s
	}
	if newest != nil {
		s := newest.Format("2006-01-02")
		cov.NewestDate = &s
	}
	return &cov, nil
}

// findBackfillWindow returns the next 7-day window to fetch.
// Strategy:
//  1. If newest < T-2 → fill forward from newest+1 to T-2 (T-1 not yet available)
//  2. If oldest > twoYearsAgo → fill backwards: 7 days before oldest
//  3. Otherwise fully covered → return zero times
func findBackfillWindow(ctx context.Context, pool *pgxpool.Pool) (start, end time.Time, ok bool) {
	latest := truncDay(time.Now().AddDate(0, 0, -2))      // T-2: yesterday's data not yet available
	twoYearsAgo := truncDay(time.Now().AddDate(-2, 0, 1)) // API rejects dates older than today-2y; oldest available is today-2y+1d

	var oldest, newest *time.Time
	pool.QueryRow(ctx, `
		SELECT MIN(DATE(interval_start)), MAX(DATE(interval_start))
		FROM usage_intervals`).
		Scan(&oldest, &newest)

	// Case 1: missing recent data
	if newest == nil || truncDay(*newest).Before(latest) {
		if newest == nil {
			start = latest
		} else {
			start = truncDay(*newest).AddDate(0, 0, 1)
		}
		end = latest
		if end.Sub(start) > 6*24*time.Hour {
			end = start.AddDate(0, 0, 6)
		}
		return start, end, true
	}

	// Case 2: need to backfill older history
	if oldest == nil || truncDay(*oldest).After(twoYearsAgo) {
		if oldest == nil {
			end = latest
		} else {
			end = truncDay(*oldest).AddDate(0, 0, -1)
		}
		if end.Before(twoYearsAgo) {
			return time.Time{}, time.Time{}, false
		}
		start = end.AddDate(0, 0, -6)
		if start.Before(twoYearsAgo) {
			start = twoYearsAgo
		}
		return start, end, true
	}

	return time.Time{}, time.Time{}, false
}

func truncDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
