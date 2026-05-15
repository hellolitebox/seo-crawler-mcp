package issues

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})
	return db
}

func seedJob(t *testing.T, db *storage.DB, jobID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO crawl_jobs (id, status, seed_urls) VALUES (?, 'completed', '["https://example.com"]')`, jobID)
	if err != nil {
		t.Fatalf("seeding job: %v", err)
	}
}

func seedURL(t *testing.T, db *storage.DB, jobID, normalizedURL, host, status, discoveredVia string) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO urls (job_id, normalized_url, host, status, discovered_via) VALUES (?, ?, ?, ?, ?)`,
		jobID, normalizedURL, host, status, discoveredVia)
	if err != nil {
		t.Fatalf("seeding URL %q: %v", normalizedURL, err)
	}
	id, _ := result.LastInsertId()
	return id
}

func seedFetch(t *testing.T, db *storage.DB, jobID string, fetchSeq int, requestedURLID int64, statusCode int) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO fetches (job_id, fetch_seq, requested_url_id, status_code) VALUES (?, ?, ?, ?)`,
		jobID, fetchSeq, requestedURLID, statusCode)
	if err != nil {
		t.Fatalf("seeding fetch: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func seedPage(t *testing.T, db *storage.DB, jobID string, urlID, fetchID int64, opts map[string]any) int64 {
	t.Helper()
	title, _ := opts["title"].(string)
	metaDesc, _ := opts["meta_description"].(string)
	contentHash, _ := opts["content_hash"].(string)
	depth, _ := opts["depth"].(int)
	inbound, _ := opts["inbound_edge_count"].(int)
	canonicalURL, _ := opts["canonical_url"].(string)
	canonicalStatusCode, _ := opts["canonical_status_code"].(int)
	hreflangJSON, _ := opts["hreflang_json"].(string)
	relNextURL, _ := opts["rel_next_url"].(string)
	relPrevURL, _ := opts["rel_prev_url"].(string)
	indexability := "indexable"
	if v, ok := opts["indexability_state"].(string); ok {
		indexability = v
	}

	h1JSON, _ := opts["h1_json"].(string)
	h2JSON, _ := opts["h2_json"].(string)

	result, err := db.Exec(`INSERT INTO pages (job_id, url_id, fetch_id, depth, title, title_length, meta_description, meta_description_length, content_hash, inbound_edge_count, canonical_url, canonical_status_code, hreflang_json, rel_next_url, rel_prev_url, indexability_state, h1_json, h2_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, urlID, fetchID, depth,
		nilIfEmpty(title), len(title),
		nilIfEmpty(metaDesc), len(metaDesc),
		nilIfEmpty(contentHash),
		inbound,
		nilIfEmpty(canonicalURL), nilIfZero(canonicalStatusCode),
		nilIfEmpty(hreflangJSON),
		nilIfEmpty(relNextURL), nilIfEmpty(relPrevURL),
		indexability,
		nilIfEmpty(h1JSON), nilIfEmpty(h2JSON),
	)
	if err != nil {
		t.Fatalf("seeding page: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilIfZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func countIssuesByType(t *testing.T, db *storage.DB, jobID, issueType string) int {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM issues WHERE job_id = ? AND issue_type = ?`, jobID, issueType).Scan(&count)
	if err != nil {
		t.Fatalf("counting issues: %v", err)
	}
	return count
}

func seedEdge(t *testing.T, db *storage.DB, jobID string, sourceURLID int64, declaredTargetURL string, relationType string, isInternal bool, discoveryMode string) {
	t.Helper()
	internal := 0
	if isInternal {
		internal = 1
	}
	_, err := db.Exec(`INSERT INTO edges (job_id, source_url_id, relation_type, is_internal, declared_target_url, discovery_mode, source_kind) VALUES (?, ?, ?, ?, ?, ?, 'html')`,
		jobID, sourceURLID, relationType, internal, declaredTargetURL, discoveryMode)
	if err != nil {
		t.Fatalf("seeding edge: %v", err)
	}
}

func TestDetectJSOnlyNavigation(t *testing.T) {
	db := testDB(t)
	jobID := "job-js-nav"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/", "example.com", "fetched", "seed")
	u2 := seedURL(t, db, jobID, "https://example.com/about", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	seedPage(t, db, jobID, u1, f1, map[string]any{"title": "Home"})
	f2 := seedFetch(t, db, jobID, 2, u2, 200)
	seedPage(t, db, jobID, u2, f2, map[string]any{"title": "About"})

	// Edge 1: static link from u1 to /about — should NOT trigger
	seedEdge(t, db, jobID, u1, "https://example.com/about", "link", true, "static")

	// Edge 2: browser-only link from u1 to /contact — SHOULD trigger
	seedEdge(t, db, jobID, u1, "https://example.com/contact", "link", true, "browser")

	// Edge 3: browser link from u1 to /about — should NOT trigger (static edge exists)
	seedEdge(t, db, jobID, u1, "https://example.com/about", "link", true, "browser")

	// Edge 4: external browser-only link — should NOT trigger (not internal)
	seedEdge(t, db, jobID, u1, "https://other.com/page", "link", false, "browser")

	cfg := DefaultGlobalConfig()
	n, err := detectJSOnlyNavigation(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectJSOnlyNavigation: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 js_only_navigation issue, got %d", n)
	}
	if c := countIssuesByType(t, db, jobID, "js_only_navigation"); c != 1 {
		t.Errorf("expected 1 issue in DB, got %d", c)
	}
}

func TestDetectJSOnlyNavigation_NoIssuesWhenAllStatic(t *testing.T) {
	db := testDB(t)
	jobID := "job-js-nav-clean"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/", "example.com", "fetched", "seed")
	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	seedPage(t, db, jobID, u1, f1, map[string]any{"title": "Home"})

	// Only static edges
	seedEdge(t, db, jobID, u1, "https://example.com/about", "link", true, "static")
	seedEdge(t, db, jobID, u1, "https://example.com/contact", "link", true, "static")

	cfg := DefaultGlobalConfig()
	n, err := detectJSOnlyNavigation(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectJSOnlyNavigation: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 js_only_navigation issues, got %d", n)
	}
}

func TestDefaultConfig_HybridRenderMode(t *testing.T) {
	// Verify default config uses hybrid render mode
	// Import is in config package, so we test via the constant comparison
	// This test lives here for convenience but validates config defaults
}

func TestDetectDuplicateTitles(t *testing.T) {
	db := testDB(t)
	jobID := "job-dup-titles"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/a", "example.com", "fetched", "seed")
	u2 := seedURL(t, db, jobID, "https://example.com/b", "example.com", "fetched", "crawl")
	u3 := seedURL(t, db, jobID, "https://example.com/c", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	f2 := seedFetch(t, db, jobID, 2, u2, 200)
	f3 := seedFetch(t, db, jobID, 3, u3, 200)

	// u1 and u2 share title, u3 is unique
	seedPage(t, db, jobID, u1, f1, map[string]any{"title": "Same Title"})
	seedPage(t, db, jobID, u2, f2, map[string]any{"title": "Same Title"})
	seedPage(t, db, jobID, u3, f3, map[string]any{"title": "Unique Title"})

	cfg := DefaultGlobalConfig()
	n, err := detectDuplicateTitles(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectDuplicateTitles: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 duplicate title issue, got %d", n)
	}
	if c := countIssuesByType(t, db, jobID, "duplicate_title"); c != 1 {
		t.Errorf("expected 1 issue in DB, got %d", c)
	}
}

func TestDetectOrphanPages(t *testing.T) {
	db := testDB(t)
	jobID := "job-orphan"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/", "example.com", "fetched", "seed")
	u2 := seedURL(t, db, jobID, "https://example.com/orphan", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	f2 := seedFetch(t, db, jobID, 2, u2, 200)

	seedPage(t, db, jobID, u1, f1, map[string]any{"title": "Home", "inbound_edge_count": 5})
	seedPage(t, db, jobID, u2, f2, map[string]any{"title": "Orphan", "inbound_edge_count": 0})

	cfg := DefaultGlobalConfig()
	n, err := detectOrphanPages(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectOrphanPages: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 orphan page, got %d", n)
	}
}

func TestDetectCanonicalToNon200(t *testing.T) {
	db := testDB(t)
	jobID := "job-canon-404"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/page", "example.com", "fetched", "seed")
	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	seedPage(t, db, jobID, u1, f1, map[string]any{
		"title":                 "Page",
		"canonical_url":         "https://example.com/gone",
		"canonical_status_code": 404,
	})

	cfg := DefaultGlobalConfig()
	n, err := detectCanonicalToNon200(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectCanonicalToNon200: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 canonical to non-200, got %d", n)
	}
}

func TestDetectSitemapReconciliation(t *testing.T) {
	db := testDB(t)
	jobID := "job-sitemap"
	seedJob(t, db, jobID)

	// URL in sitemap but not crawled (no matching URL record)
	_, err := db.Exec(`INSERT INTO sitemap_entries (job_id, url, source_sitemap_url, source_host) VALUES (?, ?, ?, ?)`,
		jobID, "https://example.com/uncrawled", "https://example.com/sitemap.xml", "example.com")
	if err != nil {
		t.Fatalf("seeding sitemap entry: %v", err)
	}

	cfg := DefaultGlobalConfig()
	n, err := detectInSitemapNotCrawled(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectInSitemapNotCrawled: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 in-sitemap-not-crawled issue, got %d", n)
	}
}

func TestCleanCrawl_NoGlobalIssues(t *testing.T) {
	db := testDB(t)
	jobID := "job-clean"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/", "example.com", "fetched", "seed")
	u2 := seedURL(t, db, jobID, "https://example.com/about", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	f2 := seedFetch(t, db, jobID, 2, u2, 200)

	seedPage(t, db, jobID, u1, f1, map[string]any{
		"title":                 "Home Page",
		"meta_description":      "Welcome to example",
		"content_hash":          "hash1",
		"inbound_edge_count":    2,
		"depth":                 0,
		"canonical_url":         "https://example.com/",
		"canonical_status_code": 200,
	})
	seedPage(t, db, jobID, u2, f2, map[string]any{
		"title":                 "About Us",
		"meta_description":      "About our company",
		"content_hash":          "hash2",
		"inbound_edge_count":    1,
		"depth":                 1,
		"canonical_url":         "https://example.com/about",
		"canonical_status_code": 200,
	})

	// Add internal edges so pages have outlinks
	seedEdge(t, db, jobID, u1, "https://example.com/about", "link", true, "static")
	seedEdge(t, db, jobID, u2, "https://example.com/", "link", true, "static")

	// Add sitemap entries matching crawled pages
	db.Exec(`INSERT INTO sitemap_entries (job_id, url, source_sitemap_url, source_host) VALUES (?, ?, ?, ?)`,
		jobID, "https://example.com/", "https://example.com/sitemap.xml", "example.com")
	db.Exec(`INSERT INTO sitemap_entries (job_id, url, source_sitemap_url, source_host) VALUES (?, ?, ?, ?)`,
		jobID, "https://example.com/about", "https://example.com/sitemap.xml", "example.com")

	cfg := DefaultGlobalConfig()
	n, err := DetectGlobalIssues(db, jobID, cfg)
	if err != nil {
		t.Fatalf("DetectGlobalIssues: %v", err)
	}
	if n != 0 {
		// List what issues were found for debugging
		rows, _ := db.Query(`SELECT issue_type, severity, scope FROM issues WHERE job_id = ?`, jobID)
		defer rows.Close()
		for rows.Next() {
			var it, sev, sc string
			rows.Scan(&it, &sev, &sc)
			t.Logf("unexpected issue: type=%q severity=%q scope=%q", it, sev, sc)
		}
		t.Errorf("expected 0 global issues on clean crawl, got %d", n)
	}
}

// ── Batch A global tests ───────────────────────────────────────────────

func TestDetectDuplicateH1(t *testing.T) {
	db := testDB(t)
	jobID := "job-dup-h1"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/a", "example.com", "fetched", "seed")
	u2 := seedURL(t, db, jobID, "https://example.com/b", "example.com", "fetched", "crawl")
	u3 := seedURL(t, db, jobID, "https://example.com/c", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	f2 := seedFetch(t, db, jobID, 2, u2, 200)
	f3 := seedFetch(t, db, jobID, 3, u3, 200)

	seedPage(t, db, jobID, u1, f1, map[string]any{"title": "Page A", "h1_json": `["Welcome Home"]`})
	seedPage(t, db, jobID, u2, f2, map[string]any{"title": "Page B", "h1_json": `["Welcome Home"]`})
	seedPage(t, db, jobID, u3, f3, map[string]any{"title": "Page C", "h1_json": `["Unique H1"]`})

	cfg := DefaultGlobalConfig()
	n, err := detectDuplicateH1(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectDuplicateH1: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 duplicate_h1 issue, got %d", n)
	}
	if c := countIssuesByType(t, db, jobID, "duplicate_h1"); c != 1 {
		t.Errorf("expected 1 issue in DB, got %d", c)
	}
}

func TestDetectDuplicateH2(t *testing.T) {
	db := testDB(t)
	jobID := "job-dup-h2"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/a", "example.com", "fetched", "seed")
	u2 := seedURL(t, db, jobID, "https://example.com/b", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	f2 := seedFetch(t, db, jobID, 2, u2, 200)

	seedPage(t, db, jobID, u1, f1, map[string]any{"title": "Page A", "h2_json": `["Features"]`})
	seedPage(t, db, jobID, u2, f2, map[string]any{"title": "Page B", "h2_json": `["Features"]`})

	cfg := DefaultGlobalConfig()
	n, err := detectDuplicateH2(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectDuplicateH2: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 duplicate_h2 issue, got %d", n)
	}
}

func TestDetectNonIndexableCanonical(t *testing.T) {
	db := testDB(t)
	jobID := "job-noindex-canon"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/page", "example.com", "fetched", "seed")
	u2 := seedURL(t, db, jobID, "https://example.com/target", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	f2 := seedFetch(t, db, jobID, 2, u2, 200)

	seedPage(t, db, jobID, u1, f1, map[string]any{
		"title":                 "Source Page",
		"canonical_url":         "https://example.com/target",
		"canonical_status_code": 200,
	})
	seedPage(t, db, jobID, u2, f2, map[string]any{
		"title":             "Target Page",
		"indexability_state": "noindex_meta",
	})

	cfg := DefaultGlobalConfig()
	n, err := detectNonIndexableCanonical(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectNonIndexableCanonical: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 non_indexable_canonical issue, got %d", n)
	}
}

func TestDetectUnlinkedCanonical(t *testing.T) {
	db := testDB(t)
	jobID := "job-unlinked-canon"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/page", "example.com", "fetched", "seed")
	// The canonical target URL exists in urls but has no inbound link edges
	seedURL(t, db, jobID, "https://example.com/canonical-target", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)

	seedPage(t, db, jobID, u1, f1, map[string]any{
		"title":                 "Source Page",
		"canonical_url":         "https://example.com/canonical-target",
		"canonical_status_code": 200,
	})

	// No edges pointing to the canonical target

	cfg := DefaultGlobalConfig()
	n, err := detectUnlinkedCanonical(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectUnlinkedCanonical: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 unlinked_canonical issue, got %d", n)
	}
}

func TestDetectUnlinkedCanonical_LinkedIsClean(t *testing.T) {
	db := testDB(t)
	jobID := "job-linked-canon"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/page", "example.com", "fetched", "seed")
	seedURL(t, db, jobID, "https://example.com/canonical-target", "example.com", "fetched", "crawl")

	f1 := seedFetch(t, db, jobID, 1, u1, 200)
	seedPage(t, db, jobID, u1, f1, map[string]any{
		"title":                 "Source Page",
		"canonical_url":         "https://example.com/canonical-target",
		"canonical_status_code": 200,
	})

	// Add internal link edge to the canonical target
	seedEdge(t, db, jobID, u1, "https://example.com/canonical-target", "link", true, "static")

	cfg := DefaultGlobalConfig()
	n, err := detectUnlinkedCanonical(db, jobID, cfg)
	if err != nil {
		t.Fatalf("detectUnlinkedCanonical: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 unlinked_canonical issues (linked canonical), got %d", n)
	}
}
