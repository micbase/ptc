package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed all:dist
var frontendFS embed.FS

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	// Start SMT backfill if credentials are configured.
	var smtClient *SMTClient
	smtUser := os.Getenv("SMT_USERNAME")
	smtPass := os.Getenv("SMT_PASSWORD")
	smtESIID := os.Getenv("SMT_ESIID")
	if smtUser != "" && smtPass != "" && smtESIID != "" {
		smtClient = NewSMTClient(smtUser, smtPass, smtESIID)
		go RunSMTBackfill(ctx, smtClient, pool)
		log.Printf("SMT backfill started for ESIID %s", smtESIID)
	} else {
		log.Printf("SMT_USERNAME/SMT_PASSWORD/SMT_ESIID not set — usage backfill disabled")
	}

	r := chi.NewRouter()

	r.Get("/api/plans", handlePlans(pool))
	r.Get("/api/charts", handleCharts(pool))
	r.Get("/api/latest-date", handleLatestDate(pool))
	r.Post("/api/fetch", handleFetch(pool))
	r.Get("/api/usage/status", handleUsageStatus(pool, smtClient))
	r.Post("/api/usage/backfill", handleUsageBackfill(pool, smtClient))
	r.Get("/api/usage/monthly", handleUsageMonthly(pool))
	r.Get("/api/usage/avg", handleUsageAvg(pool))

	distFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(distFS))

	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		f, err := distFS.Open(r.URL.Path[1:])
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fallback to index.html for SPA routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	go scheduleDailyFetch(pool)

	log.Printf("Listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

// scheduleDailyFetch runs fetchAndInsert every day at 19:10.
func scheduleDailyFetch(pool *pgxpool.Pool) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 19, 10, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		log.Printf("Daily fetch scheduled at %s", next.Format(time.RFC3339))
		time.Sleep(time.Until(next))

		log.Println("Running scheduled daily fetch")
		result, err := fetchAndInsert(context.Background(), pool)
		if err != nil {
			log.Printf("Scheduled fetch error: %v", err)
		} else {
			log.Printf("Scheduled fetch complete: %s", result.Message)
		}
	}
}
