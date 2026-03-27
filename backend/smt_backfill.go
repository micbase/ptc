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
//   - Every 2 hours: fetch the next 7-day window (fills recent gaps first,
//     then works backwards up to 2 years).
//   - With 24 API calls/day limit and 7-day batches, a full 2-year backfill
//     completes in ~5 days. The 2-hour interval uses only 12 calls/day,
//     leaving headroom.
//
// The goroutine stops when ctx is cancelled (i.e., on server shutdown).
func RunSMTBackfill(ctx context.Context, client *SMTClient, pool *pgxpool.Pool) {
	// Run once immediately on startup, then every 2 hours.
	runOnce := make(chan struct{}, 1)
	runOnce <- struct{}{}

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-runOnce:
			doBackfillStep(ctx, client, pool)
		case <-ticker.C:
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
