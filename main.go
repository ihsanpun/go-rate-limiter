package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type RateLimiter interface {
	Allow(clientID string) bool
	SetLimit(clientID string, requests int, window time.Duration)
	GetStats(clientID string) *ClientStats
	Reset(clientID string)
}

type ClientStats struct {
	ClientID      string    `json:"client_id"`
	RequestsUsed  int       `json:"requests_used"`
	RequestsLimit int       `json:"requests_limit"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
	NextReset     time.Time `json:"next_reset"`
}

type ClientConfig struct {
	RequestPerWindow int           `json:"request_per_window"`
	WindowDuration   time.Duration `json:"window_duration"`
}

type clientState struct {
	config        ClientConfig
	requestsCount int
	windowStart   time.Time
	mutex         sync.RWMutex
}

type FixedWindowRateLimiter struct {
	clients map[string]*clientState
	mutex   sync.RWMutex
	logger  *log.Logger
}

func NewFixedWindowRateLimiter(logger *log.Logger) *FixedWindowRateLimiter {
	if logger == nil {
		logger = log.Default()
	}
	return &FixedWindowRateLimiter{
		clients: make(map[string]*clientState),
		logger:  logger,
	}
}

func (rl *FixedWindowRateLimiter) Allow(clientID string) bool {
	if clientID == "" {
		return false
	}
	rl.mutex.RLock()
	client, exists := rl.clients[clientID]
	rl.mutex.RUnlock()

	if !exists {
		rl.SetLimit(clientID, 100, time.Minute)
		rl.mutex.RLock()
		client = rl.clients[clientID]
		rl.mutex.RUnlock()
	}

	client.mutex.Lock()
	defer client.mutex.Unlock()

	now := time.Now()
	if now.Sub(client.windowStart) >= client.config.WindowDuration {
		client.requestsCount = 0
		client.windowStart = now
		rl.logger.Printf("Resetting window for client %s", clientID)
	}

	if client.requestsCount >= client.config.RequestPerWindow {
		rl.logger.Printf("Rate limit exceeded for client %s: %d/%d", clientID, client.requestsCount, client.config.RequestPerWindow)
		return false
	}

	client.requestsCount++
	rl.logger.Printf("Request allowed for client %s: %d/%d", clientID, client.requestsCount, client.config.RequestPerWindow)
	return true
}

func (rl *FixedWindowRateLimiter) SetLimit(clientID string, requests int, window time.Duration) {
	if clientID == "" || requests < 0 || window <= 0 {
		return
	}

	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	if client, exists := rl.clients[clientID]; exists {
		client.mutex.Lock()
		client.config.RequestPerWindow = requests
		client.config.WindowDuration = window
		client.mutex.Unlock()
	} else {
		rl.clients[clientID] = &clientState{
			config: ClientConfig{
				RequestPerWindow: requests,
				WindowDuration:   window,
			},
			requestsCount: 0,
			windowStart:   time.Now(),
		}
	}
}

func (rl *FixedWindowRateLimiter) GetStats(clientID string) *ClientStats {
	if clientID == "" {
		return nil
	}

	rl.mutex.RLock()
	client, exists := rl.clients[clientID]
	rl.mutex.RUnlock()

	if !exists {
		return nil
	}

	client.mutex.RLock()
	defer client.mutex.RUnlock()

	now := time.Now()
	stats := &ClientStats{
		ClientID:      clientID,
		RequestsUsed:  client.requestsCount,
		RequestsLimit: client.config.RequestPerWindow,
		WindowStart:   client.windowStart,
		WindowEnd:     client.windowStart.Add(client.config.WindowDuration),
		NextReset:     client.windowStart.Add(client.config.WindowDuration),
	}

	if now.Sub(client.windowStart) >= client.config.WindowDuration {
		stats.RequestsUsed = 0
		stats.WindowStart = now
		stats.WindowEnd = now.Add(client.config.WindowDuration)
		stats.NextReset = now.Add(client.config.WindowDuration)
	}

	return stats
}

func (rl *FixedWindowRateLimiter) Reset(clientID string) {
	if clientID == "" {
		return
	}

	rl.mutex.RLock()
	client, exists := rl.clients[clientID]
	rl.mutex.RUnlock()

	if !exists {
		return
	}

	client.mutex.Lock()
	client.requestsCount = 0
	client.windowStart = time.Now()
	client.mutex.Unlock()

	rl.logger.Printf("Rate limit reset for client %s", clientID)
}

func RateLimitMiddleware(rateLimiter RateLimiter, getClientID func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientID := getClientID(r)
			if clientID == "" {
				http.Error(w, "Client identification required", http.StatusBadRequest)
				return
			}

			if !rateLimiter.Allow(clientID) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				stats := rateLimiter.GetStats(clientID)
				response := map[string]interface{}{
					"error":       "Rate limit exceeded",
					"client_id":   clientID,
					"retry_after": int(stats.NextReset.Sub(time.Now()).Seconds()),
					"limit":       stats.RequestsLimit,
					"used":        stats.RequestsUsed,
					"next_reset":  stats.NextReset.Format(time.RFC3339),
				}
				json.NewEncoder(w).Encode(response)
				return
			}

			stats := rateLimiter.GetStats(clientID)
			if stats != nil {
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(stats.RequestsLimit))
				w.Header().Set("X-RateLimit-Used", strconv.Itoa(stats.RequestsUsed))
				w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(stats.RequestsLimit-stats.RequestsUsed))
				w.Header().Set("X-RateLimit-Reset", stats.NextReset.Format(time.RFC3339))
			}

			next.ServeHTTP(w, r)
		})
	}
}

type Server struct {
	rateLimiter RateLimiter
	router      *mux.Router
	logger      *log.Logger
}

func (s *Server) setupRoutes() {
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
	s.router.Use(RateLimitMiddleware(s.rateLimiter, getClientID))

	s.router.HandleFunc("/api/test", s.handleTest).Methods("GET")
	s.router.HandleFunc("/api/data", s.handleData).Methods("GET", "POST")
	s.router.HandleFunc("/api/stats", s.handleGetStats).Methods("GET")

	adminRouter := s.router.PathPrefix("/admin").Subrouter()
	adminRouter.HandleFunc("/stats/{client_id}", s.handleGetStats).Methods("GET")
	adminRouter.HandleFunc("/limits/{client_id}", s.handleSetLimit).Methods("PUT")
	adminRouter.HandleFunc("/reset/{client_id}", s.handleReset).Methods("POST")
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var response map[string]interface{}
	if r.Method == "GET" {
		response = map[string]interface{}{
			"data":      []string{"item1", "item2", "item3"},
			"timestamp": time.Now().Format(time.RFC3339),
			"method":    "GET",
		}
	} else {
		response = map[string]interface{}{
			"message":   "Data received",
			"timestamp": time.Now().Format(time.RFC3339),
			"method":    "POST",
		}
	}

	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"message":   "Request successful!",
		"timestamp": time.Now().Format(time.RFC3339),
		"path":      r.URL.Path,
		"method":    r.Method,
	}
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientID := vars["client_id"]

	w.Header().Set("Content-Type", "application/json")
	stats := s.rateLimiter.GetStats(clientID)

	if stats == nil {
		http.Error(w, "Client not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleSetLimit(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientID := vars["client_id"]

	var config ClientConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if config.RequestPerWindow <= 0 {
		http.Error(w, "Requests per window must be positive", http.StatusBadRequest)
		return
	}

	if config.WindowDuration <= 0 {
		http.Error(w, "Window duration must be positive", http.StatusBadRequest)
		return
	}

	s.rateLimiter.SetLimit(clientID, config.RequestPerWindow, config.WindowDuration)

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"message":   "Rate Limit Updated",
		"client_id": clientID,
		"new_limit": config.RequestPerWindow,
	}
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientID := vars["client_id"]

	s.rateLimiter.Reset(clientID)

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"message":   "Rate limit reset",
		"client_id": clientID,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	json.NewEncoder(w).Encode(response)
}

func NewServer(rateLimiter RateLimiter, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}

	server := &Server{
		rateLimiter: rateLimiter,
		router:      mux.NewRouter(),
		logger:      logger,
	}

	server.setupRoutes()
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func main() {
	logger := log.New(log.Writer(), "[RATE-LIMITER] ", log.LstdFlags|log.Lshortfile)

	rateLimiter := NewFixedWindowRateLimiter(logger)

	rateLimiter.SetLimit("client1", 10, time.Minute)
	rateLimiter.SetLimit("client2", 50, time.Minute)
	rateLimiter.SetLimit("premium", 200, time.Minute)

	server := NewServer(rateLimiter, logger)

	port := ":8080"
	logger.Printf("Starting rate limiter server on port %s", port)
	if err := http.ListenAndServe(port, server); err != nil {
		logger.Fatal("Server failed to start:", err)
	}
}
