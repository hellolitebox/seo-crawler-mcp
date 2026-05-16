package httpserver

import (
	"context"
	"database/sql"
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

func TestLoadReportExtrasKeepsCrossSectionDataAvailable(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer db.Close()

	jobID := "job-cross-section"
	seedJob(t, db, jobID, "https://pipapou.com/", "completed")

	pageURLID := insertReportExtraURL(t, db, jobID, "https://www.pipapou.com/", "www.pipapou.com", "fetched", "seed")
	httpURLID := insertReportExtraURL(t, db, jobID, "http://www.pipapou.com/", "www.pipapou.com", "fetched", "http_https_audit")
	imageURLID := insertReportExtraURL(t, db, jobID, "https://www.pipapou.com/_next/image?url=%2Fhero.webp&w=3840&q=75", "www.pipapou.com", "discovered", "asset")
	scriptURLID := insertReportExtraURL(t, db, jobID, "https://www.pipapou.com/app.js", "www.pipapou.com", "discovered", "asset")

	fullFetchID := insertReportExtraFetch(t, db, jobID, 1, pageURLID, pageURLID, 200, "full", "{\"Strict-Transport-Security\":[\"max-age=31536000\"],\"Content-Security-Policy\":[\"default-src 'self'\"]}")
	httpAuditFetchID := insertReportExtraFetch(t, db, jobID, 2, httpURLID, pageURLID, 200, "http_https_audit", "{\"Strict-Transport-Security\":[\"max-age=31536000\"]}")

	if _, err := db.Exec("INSERT INTO pages (job_id, url_id, fetch_id, depth, indexability_state) VALUES (?, ?, ?, 0, 'indexable')", jobID, pageURLID, fullFetchID); err != nil {
		t.Fatalf("inserting page: %v", err)
	}
	if _, err := db.Exec("INSERT INTO redirect_hops (job_id, fetch_id, hop_index, status_code, from_url, to_url) VALUES (?, ?, 0, 301, 'http://www.pipapou.com/', 'https://www.pipapou.com/')", jobID, httpAuditFetchID); err != nil {
		t.Fatalf("inserting redirect hop: %v", err)
	}
	if _, err := db.Exec("INSERT INTO assets (job_id, url_id, content_type, status_code, content_length) VALUES (?, ?, 'application/javascript', 200, 20000)", jobID, scriptURLID); err != nil {
		t.Fatalf("inserting script asset: %v", err)
	}
	if _, err := db.Exec("INSERT INTO asset_references (job_id, asset_url_id, source_page_url_id, reference_type) VALUES (?, ?, ?, 'img_src')", jobID, imageURLID, pageURLID); err != nil {
		t.Fatalf("inserting image asset reference: %v", err)
	}

	extras := loadReportExtras(context.Background(), db, jobID)

	assertMapWithValue(t, extras["asset_references"].([]map[string]any), "asset_url", "https://www.pipapou.com/_next/image?url=%2Fhero.webp&w=3840&q=75")
	assertMapWithValue(t, extras["assets"].([]map[string]any), "url", "https://www.pipapou.com/app.js")
	assertMapWithValue(t, extras["redirect_hops"].([]map[string]any), "from_url", "http://www.pipapou.com/")
	assertMapWithValue(t, extras["response_codes"].([]map[string]any), "fetchKind", "http_https_audit")

	security := extras["security"].([]map[string]any)
	if len(security) != 2 {
		t.Fatalf("expected security rows for full + HTTP audit fetches, got %d", len(security))
	}
	headers := security[0]["headers"].(map[string]map[string]any)
	if headers["strict-transport-security"]["present"] != true {
		t.Fatalf("expected HSTS to be present in security snapshot: %#v", headers)
	}
}

func TestLoadURLClustersFlagsDuplicateWWWVariants(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer db.Close()

	jobID := "job-variants"
	seedJob(t, db, jobID, "https://pipapou.com/", "completed")

	apexURLID := insertURLForClusterTest(t, db, jobID, "https://pipapou.com/auth/login", "pipapou.com")
	wwwURLID := insertURLForClusterTest(t, db, jobID, "https://www.pipapou.com/auth/login", "www.pipapou.com")
	apexFetchID := insertFetchForClusterTest(t, db, jobID, apexURLID)
	wwwFetchID := insertFetchForClusterTest(t, db, jobID, wwwURLID)

	insertPageForClusterTest(t, db, jobID, apexURLID, apexFetchID, "same-content")
	insertPageForClusterTest(t, db, jobID, wwwURLID, wwwFetchID, "same-content")

	clusters, issues, err := loadURLClusters(context.Background(), db, jobID)
	if err != nil {
		t.Fatalf("loadURLClusters() error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %#v", clusters)
	}
	if clusters[0]["pageKey"] != "https://pipapou.com/auth/login" {
		t.Fatalf("unexpected page key: %#v", clusters[0]["pageKey"])
	}
	if clusters[0]["hasIssue"] != true {
		t.Fatalf("expected duplicate variant issue cluster: %#v", clusters[0])
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 variant issue, got %#v", issues)
	}
	if issues[0]["issueType"] != "duplicate_url_variants" || issues[0]["severity"] != "warning" {
		t.Fatalf("unexpected issue payload: %#v", issues[0])
	}
}

func insertReportExtraURL(t *testing.T, db *storage.DB, jobID, rawURL, host, status, discoveredVia string) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO urls (job_id, normalized_url, host, status, is_internal, discovered_via) VALUES (?, ?, ?, ?, 1, ?)", jobID, rawURL, host, status, discoveredVia)
	if err != nil {
		t.Fatalf("inserting url %s: %v", rawURL, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading url id: %v", err)
	}
	return id
}

func insertReportExtraFetch(t *testing.T, db *storage.DB, jobID string, fetchSeq int, requestedURLID, finalURLID int64, statusCode int, fetchKind string, headersJSON string) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO fetches (job_id, fetch_seq, requested_url_id, final_url_id, status_code, response_headers_json, fetch_kind) VALUES (?, ?, ?, ?, ?, ?, ?)", jobID, fetchSeq, requestedURLID, finalURLID, statusCode, headersJSON, fetchKind)
	if err != nil {
		t.Fatalf("inserting fetch: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading fetch id: %v", err)
	}
	return id
}

func assertMapWithValue(t *testing.T, rows []map[string]any, key string, want any) {
	t.Helper()
	for _, row := range rows {
		if valuesEqual(row[key], want) {
			return
		}
	}
	t.Fatalf("expected row with %s=%v in %#v", key, want, rows)
}

func valuesEqual(got any, want any) bool {
	switch v := got.(type) {
	case *string:
		return v != nil && *v == want
	case *int64:
		w, ok := want.(int64)
		return ok && v != nil && *v == w
	case sql.NullString:
		return v.Valid && v.String == want
	default:
		return got == want
	}
}

func insertURLForClusterTest(t *testing.T, db *storage.DB, jobID, rawURL, host string) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO urls (job_id, normalized_url, host, status, is_internal, discovered_via) VALUES (?, ?, ?, 'fetched', 1, 'test')", jobID, rawURL, host)
	if err != nil {
		t.Fatalf("inserting url %s: %v", rawURL, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading url id: %v", err)
	}
	return id
}

func insertFetchForClusterTest(t *testing.T, db *storage.DB, jobID string, urlID int64) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO fetches (job_id, fetch_seq, requested_url_id, final_url_id, status_code, fetch_kind) VALUES (?, ?, ?, ?, 200, 'full')", jobID, urlID, urlID, urlID)
	if err != nil {
		t.Fatalf("inserting fetch: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading fetch id: %v", err)
	}
	return id
}

func insertPageForClusterTest(t *testing.T, db *storage.DB, jobID string, urlID, fetchID int64, contentHash string) {
	t.Helper()
	_, err := db.Exec("INSERT INTO pages (job_id, url_id, fetch_id, depth, indexability_state, content_hash) VALUES (?, ?, ?, 0, 'indexable', ?)", jobID, urlID, fetchID, contentHash)
	if err != nil {
		t.Fatalf("inserting page: %v", err)
	}
}
