package storage

import (
	"testing"
)

func TestPurgeJob_RemovesAcrossAllChildTables(t *testing.T) {
	db := openTempDB(t)

	// Insert a job + a fan-out of child rows so we can prove they all vanish.
	if _, err := db.Exec(`
		INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
		VALUES ('purge-me', 'spider', 'completed', '{}', '[]')
	`); err != nil {
		t.Fatalf("insert job: %v", err)
	}

	urlID, err := db.UpsertURL("purge-me", "https://example.com/", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upsert url: %v", err)
	}
	fetchID, err := db.InsertFetch(FetchInput{
		JobID:          "purge-me",
		FetchSeq:       1,
		RequestedURLID: urlID,
		StatusCode:     200,
		HTTPMethod:     "GET",
		FetchKind:      "full",
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("insert fetch: %v", err)
	}
	if _, err := db.InsertPage(PageInput{
		JobID:             "purge-me",
		URLID:             urlID,
		FetchID:           fetchID,
		Depth:             0,
		IndexabilityState: "indexable",
	}); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO redirect_hops (job_id, fetch_id, hop_index, status_code, from_url, to_url) VALUES (?, ?, 0, 301, ?, ?)`, "purge-me", fetchID, "https://example.com", "https://example.com/"); err != nil {
		t.Fatalf("insert redirect hop: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO url_pattern_groups (job_id, pattern, name, source) VALUES (?, '/blog/*', 'Blog', 'auto')`, "purge-me"); err != nil {
		t.Fatalf("insert url pattern group: %v", err)
	}
	details := `{"phase":"x","message":"y"}`
	if _, err := db.InsertEvent("purge-me", "phase", &details, nil); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	if err := db.PurgeJob("purge-me"); err != nil {
		t.Fatalf("PurgeJob: %v", err)
	}

	// The job row should be gone.
	if _, err := db.GetJob("purge-me"); err == nil {
		t.Fatal("expected job to be deleted")
	}

	// Each child table should also be empty for that job.
	for _, table := range []string{"urls", "fetches", "pages", "redirect_hops", "url_pattern_groups", "crawl_events"} {
		var n int
		row := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE job_id = ?`, "purge-me")
		if err := row.Scan(&n); err != nil {
			t.Errorf("count %s: %v", table, err)
			continue
		}
		if n != 0 {
			t.Errorf("expected %s rows to be 0 after purge, got %d", table, n)
		}
	}
}

func TestPurgeJob_LeavesOtherJobsAlone(t *testing.T) {
	db := openTempDB(t)

	for _, id := range []string{"keep-1", "kill", "keep-2"} {
		if _, err := db.Exec(`
			INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
			VALUES (?, 'spider', 'completed', '{}', '[]')
		`, id); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if _, err := db.UpsertURL(id, "https://"+id+".com/", id+".com", "fetched", true, "seed"); err != nil {
			t.Fatalf("upsert url: %v", err)
		}
	}

	if err := db.PurgeJob("kill"); err != nil {
		t.Fatalf("PurgeJob: %v", err)
	}

	for _, id := range []string{"keep-1", "keep-2"} {
		if _, err := db.GetJob(id); err != nil {
			t.Errorf("expected %s to survive purge, got: %v", id, err)
		}
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM urls WHERE job_id = ?`, id).Scan(&n)
		if n != 1 {
			t.Errorf("expected %s urls=1, got %d", id, n)
		}
	}
}

func TestListJobsPaginated_ExcludesDeletingJobs(t *testing.T) {
	db := openTempDB(t)

	for _, j := range []struct{ id, status string }{
		{"alive-1", "completed"},
		{"tombstone", "deleting"},
		{"alive-2", "running"},
	} {
		if _, err := db.Exec(`
			INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
			VALUES (?, 'spider', ?, '{}', '[]')
		`, j.id, j.status); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	jobs, err := db.ListJobsPaginated(50, 0)
	if err != nil {
		t.Fatalf("ListJobsPaginated: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 visible jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		if j.ID == "tombstone" {
			t.Fatal("'deleting' job should not be in list")
		}
	}

	total, err := db.CountJobs()
	if err != nil {
		t.Fatalf("CountJobs: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2 (excluding tombstone), got %d", total)
	}
}
