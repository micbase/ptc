package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func handlePlans(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		date := r.URL.Query().Get("date")
		if date == "" {
			http.Error(w, "missing date parameter", http.StatusBadRequest)
			return
		}

		plans, err := queryPlans(r.Context(), pool, date)
		if err != nil {
			log.Printf("plans error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(plans)
	}
}

func handleLatestDate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		date, err := queryLatestDate(r.Context(), pool)
		if err != nil {
			log.Printf("latest-date error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"date": date})
	}
}

func handleFetch(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := fetchAndInsert(r.Context(), pool)
		if err != nil {
			log.Printf("fetch error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// handleUsageBackfill triggers one backfill step immediately and returns the result + updated coverage.
func handleUsageBackfill(pool *pgxpool.Pool, client *SMTClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if client == nil {
			http.Error(w, "SMT credentials not configured", http.StatusServiceUnavailable)
			return
		}
		result := doBackfillStep(r.Context(), client, pool)
		cov, err := queryUsageCoverage(r.Context(), pool)
		if err != nil {
			log.Printf("usage backfill coverage query error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"result":   result,
			"coverage": cov,
		})
	}
}

// handleUsageStatus returns the current SMT data coverage for the configured ESIID.
func handleUsageStatus(pool *pgxpool.Pool, client *SMTClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if client == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"enabled": false,
				"message": "SMT credentials not configured",
			})
			return
		}

		cov, err := queryUsageCoverage(r.Context(), pool)
		if err != nil {
			log.Printf("usage status error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"enabled":  true,
			"coverage": cov,
		})
	}
}

func handleProjection(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ProjectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.ContractExpiration == "" {
			http.Error(w, "contract_expiration is required", http.StatusBadRequest)
			return
		}
		if req.CurrentRateCents <= 0 {
			http.Error(w, "current_rate_cents must be positive", http.StatusBadRequest)
			return
		}

		today := time.Now().Truncate(24 * time.Hour)
		results, err := computeProjection(r.Context(), pool, req, today)
		if err != nil {
			log.Printf("projection error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}

func handleCharts(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chartType := r.URL.Query().Get("type")
		if chartType != "best" && chartType != "best_3m" && chartType != "variable" {
			http.Error(w, "type must be best, best_3m, or variable", http.StatusBadRequest)
			return
		}

		points, err := queryChart(r.Context(), pool, chartType)
		if err != nil {
			log.Printf("charts error (type=%s): %v", chartType, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(points)
	}
}
