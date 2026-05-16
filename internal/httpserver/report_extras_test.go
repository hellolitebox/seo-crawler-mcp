package httpserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func TestLoadReportExtrasExposesSecurityAndMarkdownNegotiation(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer db.Close()

	jobID := "job-security"
	seedJob(t, db, jobID, "https://example.com/", "completed")

	res, err := db.Exec(`
		INSERT INTO urls (job_id, normalized_url, host, status, is_internal, discovered_via)
		VALUES (?, 'https://example.com/', 'example.com', 'fetched', 1, 'seed')
	`, jobID)
	if err != nil {
		t.Fatalf("inserting url: %v", err)
	}
	urlID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading url id: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO fetches (job_id, fetch_seq, requested_url_id, status_code, response_headers_json, fetch_kind)
		VALUES (?, 1, ?, 200, ?, 'full')
	`, jobID, urlID, `{"Content-Security-Policy":["default-src 'self'"],"X-Frame-Options":["DENY"]}`)
	if err != nil {
		t.Fatalf("inserting fetch: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO crawl_events (job_id, event_type, details_json)
		VALUES (?, 'markdown_negotiation', ?)
	`, jobID, `{"pages":[{"url":"https://example.com/","supportsMarkdown":true,"contentType":"text/markdown"}],"supported":1,"totalChecked":1,"unsupported":0}`)
	if err != nil {
		t.Fatalf("inserting markdown event: %v", err)
	}

	extras := loadReportExtras(context.Background(), db, jobID)

	security, ok := extras["security"].([]map[string]any)
	if !ok {
		t.Fatalf("security has type %T", extras["security"])
	}
	if len(security) != 1 {
		t.Fatalf("expected 1 security row, got %d", len(security))
	}
	headers := security[0]["headers"].(map[string]map[string]any)
	if headers["content-security-policy"]["present"] != true {
		t.Fatalf("expected CSP to be present, got %#v", headers["content-security-policy"])
	}
	if headers["strict-transport-security"]["present"] != false {
		t.Fatalf("expected HSTS to be missing, got %#v", headers["strict-transport-security"])
	}

	markdown, ok := extras["markdown_negotiation"].([]map[string]any)
	if !ok {
		t.Fatalf("markdown_negotiation has type %T", extras["markdown_negotiation"])
	}
	if len(markdown) != 1 || markdown[0]["totalChecked"].(float64) != 1 {
		t.Fatalf("unexpected markdown negotiation payload: %#v", markdown)
	}
}
