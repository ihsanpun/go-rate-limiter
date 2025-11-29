package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFixedWindowRateLimiter_Allow(t *testing.T) {
	logger := log.New(os.Stderr, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)

	clientID := "test-client"

	for i := 0; i < 100; i++ {
		if !limiter.Allow(clientID) {
			t.Errorf("Expected request %d to be allowed", i+1)
		}
	}

	if limiter.Allow(clientID) {
		t.Error("Expected request 101 to be denied")
	}
}

func TestFixedWindowRateLimiter_SetLimit(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)

	clientID := "test-client"
	limiter.SetLimit(clientID, 5, time.Second*10)

	for i := 0; i < 5; i++ {
		if !limiter.Allow(clientID) {
			t.Errorf("Expected request %d to be allowed", i+1)
		}
	}

	if limiter.Allow(clientID) {
		t.Error("Expected 6th request to be denied")
	}
}

func TestFixedWindowRateLimiter_WindowReset(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)

	clientID := "test-client"
	limiter.SetLimit(clientID, 2, time.Millisecond*100)

	limiter.Allow(clientID)
	limiter.Allow(clientID)

	if limiter.Allow(clientID) {
		t.Error("Expected request to be denied before window reset")
	}

	time.Sleep(time.Millisecond * 150)

	if !limiter.Allow(clientID) {
		t.Error("Expected request to be allowed after window reset")
	}
}

func TestFixedWindowRateLimiter_Reset(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)

	clientID := "test-client"
	limiter.SetLimit(clientID, 2, time.Minute)

	limiter.Allow(clientID)
	limiter.Allow(clientID)

	if limiter.Allow(clientID) {
		t.Error("Expected request to be denied before reset")
	}

	limiter.Reset(clientID)

	if !limiter.Allow(clientID) {
		t.Error("Expected request to be allowed after reset")
	}
}

func TestFixedWindowRateLimiter_GetStats(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)

	clientID := "test-client"
	limiter.SetLimit(clientID, 10, time.Minute)

	limiter.Allow(clientID)
	limiter.Allow(clientID)
	limiter.Allow(clientID)

	stats := limiter.GetStats(clientID)
	if stats == nil {
		t.Fatal("Expected stats to not be nil")
	}

	if stats.ClientID != clientID {
		t.Errorf("Expected client ID %s, got %s", clientID, stats.ClientID)
	}

	if stats.RequestsUsed != 3 {
		t.Errorf("Expected 3 requests used, got %d", stats.RequestsUsed)
	}

	if stats.RequestsLimit != 10 {
		t.Errorf("Expected limit of 10, got %d", stats.RequestsLimit)
	}
}

func TestFixedWindowRateLimiter_EmptyClientID(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)

	if limiter.Allow("") {
		t.Error("Expected empty client ID to be rejected")
	}

	stats := limiter.GetStats("")
	if stats != nil {
		t.Error("Expected stats to be nil for empty client ID")
	}
}

func TestFixedWindowRateLimiter_InvalidLimits(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)

	clientID := "invalid-test-client-unique"

	limiter.SetLimit("", 10, time.Minute)
	limiter.SetLimit(clientID, 0, time.Minute)
	limiter.SetLimit(clientID, -1, time.Minute)
	limiter.SetLimit(clientID, 10, 0)
	limiter.SetLimit(clientID, 10, -time.Minute)
	stats := limiter.GetStats(clientID)
	if stats == nil {
		t.Error("Expected client to exist after setting zero limit (valid case)")
	} else {
		if stats.RequestsLimit != 0 {
			t.Errorf("Expected RequestsLimit to be 0, got %d", stats.RequestsLimit)
		}
	}

	unusedStats := limiter.GetStats("completely-unused-client")
	if unusedStats != nil {
		t.Error("Expected stats to be nil for unused client")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)
	limiter.SetLimit("test-client", 2, time.Minute)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	getClientID := func(r *http.Request) string {
		return r.Header.Get("X-Client-ID")
	}
	middleware := RateLimitMiddleware(limiter, getClientID)
	wrappedHandler := middleware(handler)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Client-ID", "test-client")
		w := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200 for request %d, got %d", i+1, w.Code)
		}
	}

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Client-ID", "test-client")
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", w.Code)
	}
}

func TestServer_Routes(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(limiter, logger)

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for test check, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode test response: %v", err)
	}

	if response["message"] != "Request successful!" {
		t.Errorf("Expected message 'Request successful!', got %v", response["message"])
	}
}

func TestServer_SetAndGetLimits(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(limiter, logger)

	clientID := "test-client"

	config := ClientConfig{
		RequestPerWindow: 50,
		WindowDuration:   time.Minute * 5,
	}

	configJSON, _ := json.Marshal(config)
	req := httptest.NewRequest("PUT", "/admin/limits/"+clientID, bytes.NewBuffer(configJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for set limits, got %d", w.Code)
	}

	limiter.Allow(clientID)
	limiter.Allow(clientID)

	req = httptest.NewRequest("GET", "/admin/stats/"+clientID, nil)
	w = httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for get stats, got %d", w.Code)
	}

	var stats ClientStats
	if err := json.NewDecoder(w.Body).Decode(&stats); err != nil {
		t.Fatalf("Failed to decode stats response: %v", err)
	}

	if stats.ClientID != clientID {
		t.Errorf("Expected client ID %s, got %s", clientID, stats.ClientID)
	}

	if stats.RequestsUsed != 2 {
		t.Errorf("Expected 2 requests used, got %d", stats.RequestsUsed)
	}
}

func TestServer_Reset(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(limiter, logger)

	clientID := "test-client"
	limiter.SetLimit(clientID, 2, time.Minute)

	limiter.Allow(clientID)
	limiter.Allow(clientID)

	if limiter.Allow(clientID) {
		t.Error("Expected request to be denied before reset")
	}

	req := httptest.NewRequest("POST", "/admin/reset/"+clientID, nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for reset, got %d", w.Code)
	}

	if !limiter.Allow(clientID) {
		t.Error("Expected request to be allowed after reset")
	}
}

func TestServer_APIEndpoints(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)
	limiter.SetLimit("test-client", 10, time.Minute)
	server := NewServer(limiter, logger)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Client-ID", "test-client")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /api/test, got %d", w.Code)
	}

	if w.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("Expected X-RateLimit-Limit header to be set")
	}

	if w.Header().Get("X-RateLimit-Used") == "" {
		t.Error("Expected X-RateLimit-Used header to be set")
	}

	if w.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("Expected X-RateLimit-Remaining header to be set")
	}

	req = httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("X-Client-ID", "test-client")
	w = httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /api/data GET, got %d", w.Code)
	}

	req = httptest.NewRequest("POST", "/api/data", bytes.NewBufferString(`{"test": "data"}`))
	req.Header.Set("X-Client-ID", "test-client")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /api/data POST, got %d", w.Code)
	}
}

func TestConcurrentRequests(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	limiter := NewFixedWindowRateLimiter(logger)
	limiter.SetLimit("concurrent-client", 50, time.Minute)

	numGoroutines := 100
	numRequestsPerGoroutine := 10
	results := make(chan bool, numGoroutines*numRequestsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < numRequestsPerGoroutine; j++ {
				result := limiter.Allow("concurrent-client")
				results <- result
			}
		}()
	}

	allowed := 0
	denied := 0
	for i := 0; i < numGoroutines*numRequestsPerGoroutine; i++ {
		if <-results {
			allowed++
		} else {
			denied++
		}
	}

	if allowed != 50 {
		t.Errorf("Expected 50 allowed requests, got %d", allowed)
	}

	if denied != 950 {
		t.Errorf("Expected 950 denied requests, got %d", denied)
	}
}

func TestNewFixedWindowRateLimiter_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)

	if rateLimiter.logger == nil {
		t.Error("Expected logger to be initialized")
	}

	customLogger := log.New(os.Stdout, "[CUSTOM] ", log.LstdFlags)
	rateLimiter2 := NewFixedWindowRateLimiter(customLogger)

	if rateLimiter2.logger != customLogger {
		t.Error("Expected custom logger to be set")
	}
}

func TestSetLimit_EdgeCases_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)

	rateLimiter.SetLimit("zero-client", 0, time.Minute)
	allowed := rateLimiter.Allow("zero-client")
	if allowed {
		t.Error("Expected zero-client to be blocked with 0 limit")
	}
	stats := rateLimiter.GetStats("zero-client")
	if stats == nil {
		t.Error("Expected stats to be not nil")
		return
	}
	if stats.RequestsLimit != 0 {
		t.Errorf("Expected limit 0, got %d", stats.RequestsLimit)
	}

	rateLimiter.SetLimit("large-client", 1000000, time.Hour)
	stats = rateLimiter.GetStats("large-client")
	if stats == nil {
		t.Error("Expected stats to be not nil")
		return
	}
	if stats.RequestsLimit != 1000000 {
		t.Errorf("Expected limit 1000000, got %d", stats.RequestsLimit)
	}

	rateLimiter.SetLimit("short-window-client", 10, time.Nanosecond)
	stats = rateLimiter.GetStats("short-window-client")
	if stats == nil {
		t.Error("Expected stats to be not nil")
		return
	}

	rateLimiter.SetLimit("", 10, time.Minute)
	rateLimiter.SetLimit("invalid-client", -1, time.Minute)
	rateLimiter.SetLimit("invalid-client2", 10, 0)
	rateLimiter.SetLimit("invalid-client3", 10, -time.Minute)

	invalidStats := rateLimiter.GetStats("invalid-client")
	if invalidStats != nil {
		t.Error("Expected no stats for invalid client")
	}
	if stats.RequestsLimit != 10 {
		t.Errorf("Expected limit 10, got %d", stats.RequestsLimit)
	}
}

func TestWindowResetBehavior_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)

	rateLimiter.SetLimit("reset-client", 2, 10*time.Millisecond)

	if !rateLimiter.Allow("reset-client") {
		t.Error("First request should be allowed")
	}
	if !rateLimiter.Allow("reset-client") {
		t.Error("Second request should be allowed")
	}
	if rateLimiter.Allow("reset-client") {
		t.Error("Third request should be denied")
	}

	time.Sleep(15 * time.Millisecond)

	if !rateLimiter.Allow("reset-client") {
		t.Error("Request after window reset should be allowed")
	}
}

func TestServer_ErrorResponses_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(rateLimiter, logger)

	reqBody := `{"invalid": "json", "missing_fields": true`
	req := httptest.NewRequest("POST", "/admin/limit/test-client", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSetLimit(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	reqBody2 := `{"limit": 100, "window": "invalid-duration"}`
	req2 := httptest.NewRequest("POST", "/admin/limit/test-client", strings.NewReader(reqBody2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	server.handleSetLimit(w2, req2)

	if w2.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w2.Code)
	}
}

func TestGetStats_ErrorHandling_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(rateLimiter, logger)

	req := httptest.NewRequest("GET", "/admin/stats/non-existent", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestNewServer_Initialization_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(rateLimiter, logger)

	if server.rateLimiter != rateLimiter {
		t.Error("Expected server to have correct rateLimiter reference")
	}

	if server.router == nil {
		t.Error("Expected server router to be initialized")
	}

	if server.logger != logger {
		t.Error("Expected server logger to be initialized")
	}
}

func TestGetStats_NonExistentClient_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)

	stats := rateLimiter.GetStats("non-existent-client")
	if stats != nil {
		t.Error("Expected nil stats for non-existent client")
	}
}

func TestReset_NonExistentClient_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)

	rateLimiter.Reset("non-existent-client")

	stats := rateLimiter.GetStats("non-existent-client")
	if stats != nil {
		t.Error("Expected nil stats for non-existent client after reset")
	}
}

func TestAllow_EmptyClientID_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)

	allowed := rateLimiter.Allow("")
	if allowed {
		t.Error("Expected empty client ID to be blocked")
	}
}

func TestMiddleware_HeaderCombinations_Coverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)
	rateLimiter.SetLimit("test-api-key", 1, time.Minute)

	getClientID := func(r *http.Request) string {
		if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
			return apiKey
		}
		if clientID := r.Header.Get("X-Client-ID"); clientID != "" {
			return clientID
		}
		if clientID := r.URL.Query().Get("client_id"); clientID != "" {
			return clientID
		}
		return r.RemoteAddr
	}

	middleware := RateLimitMiddleware(rateLimiter, getClientID)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "test-api-key")
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 with API key, got %d", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("X-API-Key", "test-api-key")
	w2 := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429 for rate limited request, got %d", w2.Code)
	}
}

func TestHTTPHandlers_ErrorCoverage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(rateLimiter, logger)

	invalidJSON := `{"invalid": json}`
	req := httptest.NewRequest("PUT", "/admin/limits/test-client", strings.NewReader(invalidJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid JSON, got %d", w.Code)
	}

	negativeJSON := `{"requests_per_window": -1, "window_duration": 60000000000}`
	req2 := httptest.NewRequest("PUT", "/admin/limits/test-client", strings.NewReader(negativeJSON))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	server.router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for negative values, got %d", w2.Code)
	}
}

func TestCoverageBooster_EdgeCases(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	rateLimiter := NewFixedWindowRateLimiter(logger)
	server := NewServer(rateLimiter, logger)

	zeroJSON := `{"requests_per_window": 10, "window_duration": 0}`
	req := httptest.NewRequest("PUT", "/admin/limits/test-client", strings.NewReader(zeroJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for zero duration, got %d", w.Code)
	}

	rateLimiter.SetLimit("max-client", 999999999, time.Hour*24*365)
	stats := rateLimiter.GetStats("max-client")
	if stats == nil {
		t.Error("Expected stats for max-client")
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			clientID := fmt.Sprintf("concurrent-test-%d", id)
			rateLimiter.SetLimit(clientID, 5, time.Second)
			for j := 0; j < 3; j++ {
				rateLimiter.Allow(clientID)
			}
			rateLimiter.GetStats(clientID)
			rateLimiter.Reset(clientID)
		}(i)
	}
	wg.Wait()

	getClientIDFallback := func(r *http.Request) string {
		return r.RemoteAddr
	}

	middleware2 := RateLimitMiddleware(rateLimiter, getClientIDFallback)
	handler2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req3 := httptest.NewRequest("GET", "/test", nil)
	req3.RemoteAddr = "127.0.0.1:12345"
	w3 := httptest.NewRecorder()
	middleware2(handler2).ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w3.Code)
	}
}

func TestCoverageBooster_RarePathsAndLoggerVariations(t *testing.T) {
	rateLimiter1 := NewFixedWindowRateLimiter(nil)
	if rateLimiter1 == nil {
		t.Error("Expected rate limiter to be created with nil logger")
	}

	var buf bytes.Buffer
	customLogger := log.New(&buf, "[CUSTOM] ", log.LstdFlags)
	rateLimiter2 := NewFixedWindowRateLimiter(customLogger)

	rateLimiter2.SetLimit("test-custom", 1, time.Millisecond*10)
	rateLimiter2.Allow("test-custom")
	rateLimiter2.Allow("test-custom")

	logOutput := buf.String()
	if !strings.Contains(logOutput, "[CUSTOM]") {
		t.Error("Expected custom logger to be used")
	}

	rateLimiter3 := NewFixedWindowRateLimiter(customLogger)
	rateLimiter3.SetLimit("window-test", 2, time.Millisecond*50)

	if !rateLimiter3.Allow("window-test") {
		t.Error("First request should be allowed")
	}
	if !rateLimiter3.Allow("window-test") {
		t.Error("Second request should be allowed")
	}
	if rateLimiter3.Allow("window-test") {
		t.Error("Third request should be rate limited")
	}

	time.Sleep(time.Millisecond * 60)
	if !rateLimiter3.Allow("window-test") {
		t.Error("Request after window reset should be allowed")
	}
}
