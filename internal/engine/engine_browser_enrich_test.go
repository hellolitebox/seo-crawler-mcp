package engine

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
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

	page, err := parser.ParseHTML([]byte(`<!doctype html><html><head><title>Rendered</title></head><body>
		<h1>One</h1><h2>Two</h2><h3>Three</h3><h4>Four</h4><h5>Five</h5><h6>Six</h6>
		<p>This rendered body has enough words to be treated as richer content.</p>
	</body></html>`), "https://example.com/", nil)
	if err != nil {
		t.Fatalf("parsing rendered HTML: %v", err)
	}

	cfg := config.DefaultConfig()
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
}
