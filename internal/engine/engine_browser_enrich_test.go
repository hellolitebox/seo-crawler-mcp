package engine

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

func TestUpdateBrowserEnrichedPagePersistsAllHeadingLevels(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "headings.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	job, err := db.CreateJob("crawl", "{}", `["https://example.com/"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	urlID, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upserting URL: %v", err)
	}
	fetchID, err := db.InsertFetch(storage.FetchInput{
		JobID:            job.ID,
		FetchSeq:         1,
		RequestedURLID:   urlID,
		StatusCode:       200,
		ContentType:      "text/html",
		HTTPMethod:       "GET",
		FetchKind:        "full",
		RenderMode:       "static",
		ContentEncoding:  "",
		ResponseBodySize: 100,
	})
	if err != nil {
		t.Fatalf("inserting fetch: %v", err)
	}
	if _, err := db.InsertPage(storage.PageInput{
		JobID:                job.ID,
		URLID:                urlID,
		FetchID:              fetchID,
		Depth:                0,
		IndexabilityState:    "indexable",
		H1JSON:               strPtr("[]"),
		H2JSON:               strPtr("[]"),
		H3JSON:               strPtr("[]"),
		H4JSON:               strPtr("[]"),
		H5JSON:               strPtr("[]"),
		H6JSON:               strPtr("[]"),
		WordCount:            intPtr(1),
		MainContentWordCount: intPtr(1),
		ContentHash:          strPtr("static"),
	}); err != nil {
		t.Fatalf("inserting page: %v", err)
	}
	for _, issueType := range []string{"js_suspect_not_rendered", "missing_h1", "missing_h2", "thin_content", "missing_canonical"} {
		if _, err := db.InsertIssue(storage.IssueInput{
			JobID:     job.ID,
			URLID:     &urlID,
			IssueType: issueType,
			Severity:  "warning",
			Scope:     "page_local",
		}); err != nil {
			t.Fatalf("inserting issue %s: %v", issueType, err)
		}
	}

	page, err := parser.ParseHTML([]byte(`<!doctype html><html><head><title>Rendered</title></head><body>
		<h1>One</h1><h2>Two</h2><h3>Three</h3><h4>Four</h4><h5>Five</h5><h6>Six</h6>
		<p>This rendered body has enough words to be treated as richer content.</p>
	</body></html>`), "https://example.com/", nil)
	if err != nil {
		t.Fatalf("parsing rendered HTML: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.ThinContentThreshold = 5
	eng := New(EngineConfig{DB: db, Config: &cfg})
	if err := eng.updateBrowserEnrichedPage(job.ID, urlID, page); err != nil {
		t.Fatalf("updating enriched page: %v", err)
	}

	stored, err := db.GetPageByURL(job.ID, urlID)
	if err != nil {
		t.Fatalf("loading page: %v", err)
	}
	assertJSON := func(name string, got sql.NullString, want string) {
		t.Helper()
		if !got.Valid {
			t.Fatalf("%s is NULL, want %s", name, want)
		}
		if got.String != want {
			t.Fatalf("%s = %s, want %s", name, got.String, want)
		}
	}
	assertJSON("h1_json", stored.H1JSON, `["One"]`)
	assertJSON("h2_json", stored.H2JSON, `["Two"]`)
	assertJSON("h3_json", stored.H3JSON, `["Three"]`)
	assertJSON("h4_json", stored.H4JSON, `["Four"]`)
	assertJSON("h5_json", stored.H5JSON, `["Five"]`)
	assertJSON("h6_json", stored.H6JSON, `["Six"]`)
	if !stored.TextPreview.Valid || stored.TextPreview.String == "" {
		t.Fatalf("text_preview = %v, want rendered body content", stored.TextPreview)
	}

	var invalidated int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM issues
		WHERE job_id = ? AND url_id = ? AND issue_type IN ('js_suspect_not_rendered', 'missing_h1', 'missing_h2', 'thin_content')`,
		job.ID, urlID,
	).Scan(&invalidated); err != nil {
		t.Fatalf("count invalidated issues: %v", err)
	}
	if invalidated != 0 {
		t.Fatalf("invalidated issue count = %d, want 0", invalidated)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM issues WHERE job_id = ? AND url_id = ? AND issue_type = 'missing_canonical'`, job.ID, urlID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining issue: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining issue count = %d, want 1", remaining)
	}
}

func TestPersistBrowserDiscoveredEdgesQueuesRenderedLinks(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "browser-links.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	job, err := db.CreateJob("crawl", "{}", `["https://www.example.com/"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	sourceURLID, err := db.UpsertURL(job.ID, "https://docs.example.com/", "docs.example.com", "fetched", true, "html")
	if err != nil {
		t.Fatalf("upserting source URL: %v", err)
	}
	fetchID, err := db.InsertFetch(storage.FetchInput{
		JobID:          job.ID,
		FetchSeq:       1,
		RequestedURLID: sourceURLID,
		StatusCode:     200,
		ContentType:    "text/html",
		HTTPMethod:     "GET",
		FetchKind:      "full",
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("inserting source fetch: %v", err)
	}
	if _, err := db.InsertPage(storage.PageInput{
		JobID:             job.ID,
		URLID:             sourceURLID,
		FetchID:           fetchID,
		Depth:             1,
		IndexabilityState: "indexable",
		WordCount:         intPtr(0),
		ContentHash:       strPtr("static-shell"),
	}); err != nil {
		t.Fatalf("inserting source page: %v", err)
	}

	page, err := parser.ParseHTML([]byte(`<!doctype html><html><body>
		<a href="/guide/getting-started">Guide</a>
		<a href="https://external.example.net/">External</a>
	</body></html>`), "https://docs.example.com/", nil)
	if err != nil {
		t.Fatalf("parsing rendered HTML: %v", err)
	}
	scope, err := urlutil.NewScopeChecker("registrable_domain", "www.example.com", nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}
	cfg := config.DefaultConfig()
	eng := New(EngineConfig{DB: db, ScopeChecker: scope, Config: &cfg})

	discovered := eng.persistBrowserDiscoveredEdges(job.ID, sourceURLID, "https://docs.example.com/", page)
	if discovered != 1 {
		t.Fatalf("discovered = %d, want 1", discovered)
	}

	queued := eng.queueBrowserDiscoveredLinkURLs(job.ID, 10)
	if queued.Len() != 1 {
		t.Fatalf("queued links = %d, want 1", queued.Len())
	}
	item, ok := queued.Pop()
	if !ok {
		t.Fatal("queued item is missing")
	}
	if item.NormalizedURL != "https://docs.example.com/guide/getting-started" {
		t.Fatalf("queued URL = %q, want rendered docs link", item.NormalizedURL)
	}
	if item.Depth != 2 {
		t.Fatalf("queued depth = %d, want 2", item.Depth)
	}

	var externalEdges int
	if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE job_id = ? AND declared_target_url LIKE '%external.example.net%'`, job.ID).Scan(&externalEdges); err != nil {
		t.Fatalf("counting external edges: %v", err)
	}
	if externalEdges != 0 {
		t.Fatalf("external rendered edges = %d, want 0", externalEdges)
	}
}
