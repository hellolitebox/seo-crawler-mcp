package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func TestMarkdownNegotiationUsesSSRFGuard(t *testing.T) {
	var hits atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# private"))
	}))
	defer ts.Close()

	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	urlID, err := db.UpsertURL(job.ID, ts.URL, "127.0.0.1", "crawled", true, "seed")
	if err != nil {
		t.Fatalf("UpsertURL: %v", err)
	}
	fetchID, err := db.InsertFetch(storage.FetchInput{
		JobID:          job.ID,
		FetchSeq:       1,
		RequestedURLID: urlID,
		FinalURLID:     &urlID,
		StatusCode:     200,
		ContentType:    "text/html",
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("InsertFetch: %v", err)
	}
	if _, err := db.InsertPage(storage.PageInput{JobID: job.ID, URLID: urlID, FetchID: fetchID, Depth: 0}); err != nil {
		t.Fatalf("InsertPage: %v", err)
	}

	guard := ssrf.NewGuard(false)
	cfg := config.DefaultConfig()
	f := fetcher.New(fetcher.Options{
		UserAgent:           cfg.UserAgent,
		Timeout:             time.Second,
		MaxResponseBody:     cfg.MaxResponseBody,
		MaxDecompressedBody: cfg.MaxDecompressedBody,
		MaxRedirectHops:     cfg.MaxRedirectHops,
		SSRFGuard:           guard,
	})
	eng := New(EngineConfig{DB: db, Fetcher: f, SSRFGuard: guard, Config: &cfg})
	eng.checkMarkdownNegotiation(context.Background(), job.ID)

	if hits.Load() != 0 {
		t.Fatalf("markdown negotiation hit private host %d time(s)", hits.Load())
	}
}
