package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"
)

const smtBaseURL = "https://www.smartmetertexas.com"

// SMTClient handles authentication and interval data retrieval from SmartMeterTexas.
// It uses the unofficial token-based API reverse-engineered from the SMT web portal.
// Rate limit: 2 on-demand reads/hour, 24/day per ESIID.
type SMTClient struct {
	username string
	password string
	ESIID    string

	mu          sync.Mutex
	authToken   string
	tokenExpiry time.Time
	httpClient  *http.Client
}

func NewSMTClient(username, password, esiid string) *SMTClient {
	// ForceAttemptHTTP2:false — Go's HTTP/2 client triggers INTERNAL_ERROR on
	// the SMT Envoy proxy even though curl's HTTP/2 works fine. The difference
	// is Go's TLS ALPN negotiation; disabling HTTP/2 avoids the issue entirely.
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{},
		ForceAttemptHTTP2: false,
	}
	// Cookie jar works like a browser: the auth response sets Akamai cookies
	// (_abck, bm_sz); the jar stores them and automatically includes them in
	// the Cookie header on all subsequent requests to the same domain.
	jar, _ := cookiejar.New(nil)
	return &SMTClient{
		username:   username,
		password:   password,
		ESIID:      esiid,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport, Jar: jar},
	}
}

func (c *SMTClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Now().Before(c.tokenExpiry) {
		return c.authToken, nil
	}

	// Warm up: GET /home so Akamai can set its challenge cookies (_abck, bm_sz)
	// into the jar before the auth POST. Without this the auth request arrives
	// with no cookies and Akamai may challenge or block it.
	warm, _ := http.NewRequestWithContext(ctx, "GET", smtBaseURL+"/home", nil)
	setSmtHeaders(warm, "")
	if r, err := c.httpClient.Do(warm); err == nil {
		r.Body.Close()
	}

	body, _ := json.Marshal(map[string]string{
		"username":   c.username,
		"password":   c.password,
		"rememberMe": "true",
	})

	req, _ := http.NewRequestWithContext(ctx, "POST",
		smtBaseURL+"/commonapi/user/authenticate", bytes.NewReader(body))
	setSmtHeaders(req, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("smt auth: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	var ar struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(bodyBytes, &ar); err != nil {
		return "", fmt.Errorf("smt auth decode: %w", err)
	}
	if ar.Token == "" {
		return "", fmt.Errorf("smt auth: empty token in response")
	}

	c.authToken = ar.Token
	c.tokenExpiry = time.Now().Add(110 * time.Minute) // token valid for 2 hours; refresh at ~110 min
	return c.authToken, nil
}

// SMTInterval is one 15-minute usage reading.
type SMTInterval struct {
	Start          time.Time
	ConsumptionKwh float64
	IsActual       bool
}

type smtIntervalResp struct {
	IntervalData []struct {
		Date              string   `json:"date"`
		StartTime         string   `json:"starttime"`
		EndTime           string   `json:"endtime"`
		Consumption       float64  `json:"consumption"`
		ConsumptionEstAct string   `json:"consumption_est_act"`
		Generation        float64  `json:"generation"`
		GenerationEstAct  *string  `json:"generation_est_act"` // null when no solar
	} `json:"intervaldata"`
	GenerationFlag bool `json:"generationFlag"`
}

// FetchIntervals retrieves 15-minute interval data for [start, end] (inclusive).
// Dates are treated as CT local time.
func (c *SMTClient) FetchIntervals(ctx context.Context, start, end time.Time) ([]SMTInterval, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	body, _ := json.Marshal(map[string]string{
		"esiid":     c.ESIID,
		"startDate": start.Format("01/02/2006"),
		"endDate":   end.Format("01/02/2006"),
	})

	req, _ := http.NewRequestWithContext(ctx, "POST",
		smtBaseURL+"/api/usage/interval", bytes.NewReader(body))
	setSmtHeaders(req, token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("smt intervals: %w", err)
	}
	defer resp.Body.Close()

	var ir smtIntervalResp
	if err := json.NewDecoder(resp.Body).Decode(&ir); err != nil {
		return nil, fmt.Errorf("smt intervals decode: %w", err)
	}

	var intervals []SMTInterval
	for _, row := range ir.IntervalData {
		// date: "2026-03-24", starttime: " 12:00 am" (leading space, 12-hour)
		ts, err := time.Parse("2006-01-02 3:04 pm", row.Date+" "+strings.TrimSpace(row.StartTime))
		if err != nil {
			continue
		}
		intervals = append(intervals, SMTInterval{
			Start:          ts,
			ConsumptionKwh: row.Consumption,
			IsActual:       strings.ToUpper(row.ConsumptionEstAct) == "A",
		})
	}

	return intervals, nil
}

func setSmtHeaders(req *http.Request, token string) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:148.0) Gecko/20100101 Firefox/148.0")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", smtBaseURL)
	req.Header.Set("Referer", smtBaseURL+"/home")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
