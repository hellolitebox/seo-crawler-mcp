package engine

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func TestRecomputePageDepths_UsesFinalShortestPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "depth-recompute.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	seedURLs, _ := json.Marshal([]string{"https://example.com/"})
	job, err := db.CreateJob("crawl", "{}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	homeID, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upsert home: %v", err)
	}
	workID, err := db.UpsertURL(job.ID, "https://example.com/work", "example.com", "fetched", true, "link")
	if err != nil {
		t.Fatalf("upsert work: %v", err)
	}
	aboutID, err := db.UpsertURL(job.ID, "https://example.com/about-us", "example.com", "fetched", true, "link")
	if err != nil {
		t.Fatalf("upsert about: %v", err)
	}
	contactID, err := db.UpsertURL(job.ID, "https://example.com/contact-us", "example.com", "fetched", true, "link")
	if err != nil {
		t.Fatalf("upsert contact: %v", err)
	}

	mkPage := func(urlID int64, fetchSeq int, depth int) {
		fetchID, err := db.InsertFetch(storage.FetchInput{
			JobID:               job.ID,
			FetchSeq:            fetchSeq,
			RequestedURLID:      urlID,
			FinalURLID:          &urlID,
			StatusCode:          200,
			ContentType:         "text/html",
			ContentEncoding:     "",
			ResponseHeadersJSON: "{}",
			HTTPMethod:          "GET",
			FetchKind:           "full",
			RenderMode:          "static",
		})
		if err != nil {
			t.Fatalf("insert fetch for %d: %v", urlID, err)
		}
		_, err = db.InsertPage(storage.PageInput{JobID: job.ID, URLID: urlID, FetchID: fetchID, Depth: depth})
		if err != nil {
			t.Fatalf("insert page for %d: %v", urlID, err)
		}
	}

	// Simulate stale depths from discovery order.
	mkPage(homeID, 1, 0)
	mkPage(workID, 2, 2)
	mkPage(aboutID, 3, 2)
	mkPage(contactID, 4, 3)

	insertLink := func(sourceID int64, targetURL string, targetID int64) {
		_, err := db.InsertEdge(storage.EdgeInput{
			JobID:                 job.ID,
			SourceURLID:           sourceID,
			NormalizedTargetURLID: targetID,
			SourceKind:            "html",
			RelationType:          "link",
			DiscoveryMode:         "static",
			IsInternal:            true,
			DeclaredTargetURL:     targetURL,
		})
		if err != nil {
			t.Fatalf("insert edge %d -> %s: %v", sourceID, targetURL, err)
		}
	}

	// Longer path discovered first.
	insertLink(homeID, "https://example.com/work", workID)
	insertLink(workID, "https://example.com/about-us", aboutID)
	insertLink(aboutID, "https://example.com/contact-us", contactID)
	// Direct homepage links discovered later should win after recompute.
	insertLink(homeID, "https://example.com/about-us", aboutID)
	insertLink(homeID, "https://example.com/contact-us", contactID)

	eng := &Engine{db: db}
	if err := eng.recomputePageDepths(job.ID); err != nil {
		t.Fatalf("recomputePageDepths: %v", err)
	}

	assertDepth := func(url string, want int) {
		row := db.QueryRow(`
			SELECT p.depth
			FROM pages p
			JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
			WHERE p.job_id = ? AND u.normalized_url = ?
		`, job.ID, url)
		var got int
		if err := row.Scan(&got); err != nil {
			t.Fatalf("scan depth for %s: %v", url, err)
		}
		if got != want {
			t.Fatalf("depth for %s = %d, want %d", url, got, want)
		}
	}

	assertDepth("https://example.com/", 0)
	assertDepth("https://example.com/work", 1)
	assertDepth("https://example.com/about-us", 1)
	assertDepth("https://example.com/contact-us", 1)
}
