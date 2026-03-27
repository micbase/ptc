package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func dedupeIntervals(intervals []SMTInterval) []SMTInterval {
	seen := make(map[time.Time]struct{}, len(intervals))
	out := intervals[:0:0]
	for _, iv := range intervals {
		if _, dup := seen[iv.Start]; dup {
			continue
		}
		seen[iv.Start] = struct{}{}
		out = append(out, iv)
	}
	return out
}

func upsertIntervals(ctx context.Context, pool *pgxpool.Pool, intervals []SMTInterval) (int, error) {
	if len(intervals) == 0 {
		return 0, nil
	}

	intervals = dedupeIntervals(intervals)

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
// Fills forward from the oldest available date (today-2y+1d) up to T-2.
// Returns ok=false when fully covered.
func findBackfillWindow(ctx context.Context, pool *pgxpool.Pool) (start, end time.Time, ok bool) {
	oldest    := truncDay(time.Now().AddDate(-2, 0, 1)) // oldest date the API has
	yesterday := truncDay(time.Now().AddDate(0, 0, -1)) // target: T-1, available ~8pm CT

	var newest *time.Time
	pool.QueryRow(ctx, `SELECT MAX(DATE(interval_start)) FROM usage_intervals`).Scan(&newest)

	if newest == nil {
		start = oldest
	} else {
		start = truncDay(*newest).AddDate(0, 0, 1)
	}

	if !start.Before(yesterday) {
		return time.Time{}, time.Time{}, false // fully covered
	}

	end = start.AddDate(0, 0, 6)
	if end.After(yesterday) {
		end = yesterday
	}
	return start, end, true
}

func truncDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
