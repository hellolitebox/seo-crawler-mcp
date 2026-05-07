package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// newTestServer creates a Server backed by a temp-file SQLite DB.
// Returns the server, an http test handler, and a cleanup func.
func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(db, nil, nil)
	return srv, srv.Handler()
}

// seedJob inserts a completed crawl job with the given URL.
func seedJob(t *testing.T, db *storage.DB, jobID, seedURL, status string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
		VALUES (?, 'spider', ?, '{}', ?)
	`, jobID, status, `["`+seedURL+`"]`)
	if err != nil {
		t.Fatalf("seeding job: %v", err)
	}
}

func TestHealth(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body)
	}
}

func TestJobsList_EmptyDb(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Jobs   []any `json:"jobs"`
		Total  int   `json:"total"`
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp.Total != 0 {
		t.Fatalf("expected total=0, got %d", resp.Total)
	}
	if resp.Limit != 50 {
		t.Fatalf("expected default limit=50, got %d", resp.Limit)
	}
}

func TestJobsList_Pagination(t *testing.T) {
	srv, h := newTestServer(t)

	// Insert 5 completed jobs
	for i := 0; i < 5; i++ {
		seedJob(t, srv.db, string(rune('a'+i)), "https://example.com", "completed")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs?limit=2&offset=1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Jobs   []map[string]any `json:"jobs"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp.Total != 5 {
		t.Fatalf("expected total=5, got %d", resp.Total)
	}
	if len(resp.Jobs) != 2 {
		t.Fatalf("expected 2 jobs returned, got %d", len(resp.Jobs))
	}
	if resp.Limit != 2 || resp.Offset != 1 {
		t.Fatalf("expected limit=2, offset=1, got %d/%d", resp.Limit, resp.Offset)
	}
}

func TestJobsList_LimitClamps(t *testing.T) {
	_, h := newTestServer(t)

	// limit=999 should clamp to default since it's out of range (>200)
	req := httptest.NewRequest(http.MethodGet, "/api/jobs?limit=999", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var resp struct{ Limit int }
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Limit != 50 {
		t.Fatalf("expected limit clamped to 50, got %d", resp.Limit)
	}
}

func TestJobStatus_NotFound(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/does-not-exist", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestJobStatus_Found(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-1", "https://example.com", "completed")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp["jobId"] != "job-1" {
		t.Fatalf("expected jobId=job-1, got %v", resp["jobId"])
	}
	if resp["status"] != "completed" {
		t.Fatalf("expected status=completed, got %v", resp["status"])
	}
}

func TestJobDelete_TombstonesAndPurgesAsync(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-purge", "https://example.com", "completed")

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/job-purge", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "deleted" {
		t.Fatalf("expected status=deleted, got %v", resp)
	}

	// The handler returns immediately after tombstoning. The actual purge
	// happens in a background goroutine — wait briefly for it to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := srv.db.GetJob("job-purge"); err != nil {
			return // job is gone, success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected job to be purged within 2s")
}

func TestJobDelete_TombstoneHidesFromList(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "alive", "https://a.com", "completed")
	seedJob(t, srv.db, "deleting", "https://b.com", "completed")

	// Trigger DELETE on the second one.
	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/deleting", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE: expected 200, got %d", rr.Code)
	}

	// The list should immediately exclude tombstoned jobs, even before the
	// background purge finishes.
	listReq := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	var listResp struct {
		Jobs  []map[string]any `json:"jobs"`
		Total int              `json:"total"`
	}
	json.NewDecoder(listRR.Body).Decode(&listResp)
	if listResp.Total != 1 {
		t.Fatalf("expected 1 job in list, got %d", listResp.Total)
	}
	for _, j := range listResp.Jobs {
		if j["jobId"] == "deleting" {
			t.Fatal("'deleting' job should not appear in list")
		}
	}
}

func TestJobDelete_CancelsRunning(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-running", "https://example.com", "running")

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/job-running", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "cancelling" {
		t.Fatalf("expected status=cancelling, got %v", resp)
	}

	// Job should still exist but with 'cancelling' status
	job, err := srv.db.GetJob("job-running")
	if err != nil {
		t.Fatalf("expected job to still exist, got: %v", err)
	}
	if job.Status != "cancelling" {
		t.Fatalf("expected status=cancelling, got %s", job.Status)
	}
}

func TestJobDelete_NotFound(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/ghost", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestJobActivity_Empty(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-act", "https://example.com", "running")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-act/activity", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Activity []any `json:"activity"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	// Empty is fine; just ensure shape is correct.
	if resp.Activity == nil {
		t.Fatalf("expected activity array (possibly empty), got nil")
	}
}

func TestCORS_AllowedOrigin(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://seo-crawler-report.vercel.app")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://seo-crawler-report.vercel.app" {
		t.Fatalf("expected allowed origin echoed, got %q", got)
	}
}

func TestCORS_DeniedOrigin(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no ACAO header for denied origin, got %q", got)
	}
}

func TestCORS_OptionsPreflight(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/crawl", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("expected ACAM header on preflight")
	}
}
