// Package httpserver provides an HTTP API for the SEO crawler.
package httpserver

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/engine"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// Server exposes the crawler over HTTP.
type Server struct {
	db      *storage.DB
	engine  *engine.Engine
	config  *config.Config
	limiter *rateLimiter
	purger  *purgeWorker
	allowed []string // allowed CORS origins
}

// New creates a new HTTP server.
func New(db *storage.DB, eng *engine.Engine, cfg *config.Config) *Server {
	allowed := []string{
		"https://seo-crawler-report.vercel.app",
		"http://localhost:4321",
		"http://localhost:3000",
	}
	if extra := os.Getenv("ALLOWED_ORIGINS"); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowed = append(allowed, o)
			}
		}
	}
	return &Server{
		db:      db,
		engine:  eng,
		config:  cfg,
		allowed: allowed,
		limiter: newRateLimiter(10, time.Hour), // 10 crawls per IP per hour
		purger:  newPurgeWorker(db),
	}
}

// Handler returns the HTTP handler with all routes registered.
// Uses the Go 1.22+ ServeMux pattern matching for clean routing.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /api/crawl", s.rateLimitMiddleware(s.handleCrawl))
	mux.HandleFunc("GET /api/jobs", s.handleJobsList)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJobStatusV2)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.handleJobCancelV2)
	mux.HandleFunc("GET /api/jobs/{id}/report", s.handleJobReportV2)
	mux.HandleFunc("GET /api/jobs/{id}/pages", s.handleJobPagesV2)
	mux.HandleFunc("GET /api/jobs/{id}/issues", s.handleJobIssuesV2)
	mux.HandleFunc("GET /api/jobs/{id}/activity", s.handleJobActivityV2)
	mux.HandleFunc("GET /api/jobs/{id}/stream", s.handleJobStreamV2)

	return s.corsMiddleware(loggingMiddleware(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true}`))
}

// corsMiddleware sends CORS headers based on a whitelist of allowed origins.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		for _, a := range s.allowed {
			if origin == a {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				break
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs each request with method, path, and duration via slog.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start).String(),
			"ip", clientIP(r),
		)
	})
}

// rateLimitMiddleware applies the configured rate limiter to a handler.
func (s *Server) rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded: try again later")
			return
		}
		next(w, r)
	}
}

func clientIP(r *http.Request) string {
	// Trust the X-Forwarded-For from Fly's proxy.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// rateLimiter is a simple per-IP fixed-window limiter (in-memory).
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*windowState
}

type windowState struct {
	count int
	start time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{limit: limit, window: window, hits: map[string]*windowState{}}
	go rl.gc()
	return rl
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	st, ok := rl.hits[key]
	if !ok || now.Sub(st.start) >= rl.window {
		rl.hits[key] = &windowState{count: 1, start: now}
		return true
	}
	if st.count >= rl.limit {
		return false
	}
	st.count++
	return true
}

func (rl *rateLimiter) gc() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		rl.mu.Lock()
		now := time.Now()
		for k, st := range rl.hits {
			if now.Sub(st.start) >= rl.window*2 {
				delete(rl.hits, k)
			}
		}
		rl.mu.Unlock()
	}
}
