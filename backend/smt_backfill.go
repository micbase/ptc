package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunSMTBackfill starts a background goroutine that continuously fills in
// usage_intervals data from SmartMeterTexas.
//
// Strategy:
//   - Runs once immediately on startup, then twice a day at 08:00 and 20:00.
//   - Each run fetches the next 7-day window (fills recent gaps first, then
//     works backwards up to 2 years).
//   - With 24 API calls/day limit and 7-day batches, a full 2-year backfill
//     completes in ~52 days at 2 runs/day.
//
// The goroutine stops when ctx is cancelled (i.e., on server shutdown).
func RunSMTBackfill(ctx context.Context, client *SMTClient, pool *pgxpool.Pool) {
	// Run once immediately on startup.
	doBackfillStep(ctx, client, pool)

	scheduleHours := []int{8, 20}
	for {
		now := time.Now()
		var next time.Time
		for _, h := range scheduleHours {
			t := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
			if t.After(now) && (next.IsZero() || t.Before(next)) {
				next = t
			}
		}
		if next.IsZero() {
			next = time.Date(now.Year(), now.Month(), now.Day()+1, scheduleHours[0], 0, 0, 0, now.Location())
		}
		log.Printf("SMT backfill next run scheduled at %s", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			doBackfillStep(ctx, client, pool)
		}
	}
}

type BackfillResult struct {
	AlreadyCovered bool   `json:"already_covered,omitempty"`
	StartDate      string `json:"start_date,omitempty"`
	EndDate        string `json:"end_date,omitempty"`
	Fetched        int    `json:"fetched,omitempty"`
	Upserted       int    `json:"upserted,omitempty"`
	Message        string `json:"message"`
}

func doBackfillStep(ctx context.Context, client *SMTClient, pool *pgxpool.Pool) *BackfillResult {
	start, end, ok := findBackfillWindow(ctx, pool)
	if !ok {
		log.Printf("SMT backfill: coverage complete (2 years)")
		return &BackfillResult{AlreadyCovered: true, Message: "coverage complete (2 years)"}
	}

	log.Printf("SMT backfill: fetching %s → %s", start.Format("2006-01-02"), end.Format("2006-01-02"))

	intervals, err := client.FetchIntervals(ctx, start, end)
	if err != nil {
		log.Printf("SMT backfill: fetch error: %v", err)
		return &BackfillResult{Message: "fetch error: " + err.Error()}
	}

	n, err := upsertIntervals(ctx, pool, intervals)
	if err != nil {
		log.Printf("SMT backfill: db error: %v", err)
		return &BackfillResult{Message: "db error: " + err.Error()}
	}

	msg := fmt.Sprintf("upserted %d intervals for %s → %s",
		n, start.Format("2006-01-02"), end.Format("2006-01-02"))
	log.Printf("SMT backfill: %s", msg)
	return &BackfillResult{
		StartDate: start.Format("2006-01-02"),
		EndDate:   end.Format("2006-01-02"),
		Fetched:   len(intervals),
		Upserted:  n,
		Message:   msg,
	}
}
