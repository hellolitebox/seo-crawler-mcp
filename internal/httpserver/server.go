// Package httpserver provides an HTTP API for the SEO crawler.
package httpserver

import (
	"net/http"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/engine"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// Server exposes the crawler over HTTP.
type Server struct {
	db     *storage.DB
	engine *engine.Engine
	config *config.Config
}

// New creates a new HTTP server.
func New(db *storage.DB, eng *engine.Engine, cfg *config.Config) *Server {
	return &Server{db: db, engine: eng, config: cfg}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/crawl", s.handleCrawl)
	mux.HandleFunc("/api/jobs", s.handleJobsList)
	mux.HandleFunc("/api/jobs/", s.handleJobs)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	return corsMiddleware(mux)
}

// corsMiddleware adds Access-Control-Allow-Origin: * to every response.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
