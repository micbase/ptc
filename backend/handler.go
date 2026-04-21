package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
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

		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		results, err := computeSweep(r.Context(), pool, req, today)
		if err != nil {
			log.Printf("projection error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}

func handleLatestSwitchEvent(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := querySwitchEvents(r.Context(), pool, 1)
		if err != nil {
			log.Printf("latest switch event error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if len(records) == 0 {
			json.NewEncoder(w).Encode(nil)
			return
		}
		json.NewEncoder(w).Encode(records[0])
	}
}

func handleSwitchEvents(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := querySwitchEvents(r.Context(), pool, 0)
		if err != nil {
			log.Printf("switch events error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(records)
	}
}

func handleAddSwitchEvent(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AddSwitchEventRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.ElectricityRateID == 0 {
			http.Error(w, "electricity_rate_id is required", http.StatusBadRequest)
			return
		}
		if req.SwitchDate == "" {
			http.Error(w, "switch_date is required", http.StatusBadRequest)
			return
		}
		if req.ContractExpirationDate == "" {
			http.Error(w, "contract_expiration_date is required", http.StatusBadRequest)
			return
		}

		record, err := insertSwitchEvent(r.Context(), pool, req)
		if err != nil {
			log.Printf("add switch event error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(record)
	}
}

func handleUpdateSwitchEvent(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}
		var req UpdateSwitchEventRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.SwitchDate == "" {
			http.Error(w, "switch_date is required", http.StatusBadRequest)
			return
		}
		if req.ContractExpirationDate == "" {
			http.Error(w, "contract_expiration_date is required", http.StatusBadRequest)
			return
		}
		record, err := updateSwitchEvent(r.Context(), pool, id, req)
		if err != nil {
			log.Printf("update switch event error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(record)
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
