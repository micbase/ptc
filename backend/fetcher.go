package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const ptcCSVURL = "https://www.powertochoose.org/en-us/Plan/ExportToCsv"

var columnMapping = map[string]string{
	"[idKey]":               "id_key",
	"[TduCompanyName]":      "tdu_company_name",
	"[RepCompany]":          "rep_company",
	"[Product]":             "product",
	"[kwh500]":              "kwh500",
	"[kwh1000]":             "kwh1000",
	"[kwh2000]":             "kwh2000",
	"[Fees/Credits]":        "fees_credits",
	"[PrePaid]":             "prepaid",
	"[TimeOfUse]":           "time_of_use",
	"[Fixed]":               "fixed",
	"[RateType]":            "rate_type",
	"[Renewable]":           "renewable",
	"[TermValue]":           "term_value",
	"[CancelFee]":           "cancel_fee",
	"[Website]":             "website",
	"[SpecialTerms]":        "special_terms",
	"[TermsURL]":            "terms_url",
	"[YRACURL]":             "yrac_url",
	"[Promotion]":           "promotion",
	"[PromotionDesc]":       "promotion_desc",
	"[FactsURL]":            "facts_url",
	"[EnrollURL]":           "enroll_url",
	"[PrepaidURL]":          "prepaid_url",
	"[EnrollPhone]":         "enroll_phone",
	"[NewCustomer]":         "new_customer",
	"[MinUsageFeesCredits]": "min_usage_fees_credits",
	"[Language]":            "language",
	"[Rating]":              "rating",
}

var numericCols = map[string]bool{
	"kwh500": true, "kwh1000": true, "kwh2000": true,
	"renewable": true, "term_value": true, "rating": true, "fixed": true,
}

var boolCols = map[string]bool{
	"prepaid": true, "time_of_use": true, "new_customer": true,
	"min_usage_fees_credits": true, "promotion": true,
}

type FetchResult struct {
	Upserted int    `json:"upserted"`
	Message  string `json:"message"`
}

func fetchAndInsert(ctx context.Context, pool *pgxpool.Pool) (*FetchResult, error) {
	today := time.Now().Format("2006-01-02")

	log.Printf("Downloading CSV from %s", ptcCSVURL)
	resp, err := http.Get(ptcCSVURL)
	if err != nil {
		return nil, fmt.Errorf("downloading CSV: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading CSV body: %w", err)
	}

	// Filter: keep only quoted CSV lines, stop at EOF marker
	rawLines := strings.Split(string(body), "\n")
	var csvLines []string
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "END OF FILE") || strings.HasPrefix(upper, "EOF") {
			break
		}
		if strings.HasPrefix(line, `"`) {
			csvLines = append(csvLines, line)
		}
	}

	if len(csvLines) == 0 {
		return nil, fmt.Errorf("no valid CSV data found in response")
	}

	r := csv.NewReader(strings.NewReader(strings.Join(csvLines, "\n")))
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("CSV has no data rows")
	}

	// Map raw header names to DB column names; track which indices are valid
	rawHeaders := records[0]
	var validIdxs []int
	var validCols []string
	for i, h := range rawHeaders {
		h = strings.TrimSpace(h)
		if mapped, ok := columnMapping[h]; ok {
			validIdxs = append(validIdxs, i)
			validCols = append(validCols, mapped)
		}
	}

	// Full column list for INSERT
	insertCols := append(validCols, "fetch_date", "processed_at")

	placeholders := make([]string, len(insertCols))
	for i := range insertCols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	// ON CONFLICT: update all columns except the conflict key (id_key, fetch_date)
	conflictKey := map[string]bool{"id_key": true, "fetch_date": true}
	var setClauses []string
	for _, col := range insertCols {
		if !conflictKey[col] {
			setClauses = append(setClauses, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
		}
	}

	query := fmt.Sprintf(
		`INSERT INTO electricity_rates (%s) VALUES (%s)
		 ON CONFLICT (id_key, fetch_date) DO UPDATE SET %s`,
		strings.Join(insertCols, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(setClauses, ", "),
	)

	processedAt := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	upserted := 0
	for _, record := range records[1:] {
		args := make([]interface{}, len(insertCols))
		for j, idx := range validIdxs {
			val := ""
			if idx < len(record) {
				val = strings.TrimSpace(record[idx])
			}
			col := validCols[j]
			switch {
			case numericCols[col]:
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					args[j] = f
				} else {
					args[j] = 0.0
				}
			case boolCols[col]:
				args[j] = strings.ToLower(val) == "true"
			default:
				args[j] = val
			}
		}
		args[len(validIdxs)] = today
		args[len(validIdxs)+1] = processedAt

		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return nil, fmt.Errorf("upserting row %d: %w", upserted+1, err)
		}
		upserted++
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	log.Printf("Upserted %d rows for %s", upserted, today)
	return &FetchResult{
		Upserted: upserted,
		Message:  fmt.Sprintf("upserted %d rows for %s", upserted, today),
	}, nil
}
