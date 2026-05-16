package storage

import "testing"

func TestInsertAndGetPage(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	urlID, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "pending", true, "seed")
	if err != nil {
		t.Fatalf("UpsertURL: %v", err)
	}

	fetchID, err := db.InsertFetch(FetchInput{
		JobID:          job.ID,
		FetchSeq:       1,
		RequestedURLID: urlID,
		StatusCode:     200,
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("InsertFetch: %v", err)
	}

	title := "Example Page"
	titleLen := 12
	wc := 500
	textPreview := "This is visible page content."
	input := PageInput{
		JobID:             job.ID,
		URLID:             urlID,
		FetchID:           fetchID,
		Depth:             0,
		Title:             &title,
		TitleLength:       &titleLen,
		IndexabilityState: "indexable",
		WordCount:         &wc,
		TextPreview:       &textPreview,
		JSSuspect:         false,
	}

	id, err := db.InsertPage(input)
	if err != nil {
		t.Fatalf("InsertPage: %v", err)
	}

	got, err := db.GetPage(id)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}

	if got.JobID != job.ID {
		t.Errorf("expected job_id %q, got %q", job.ID, got.JobID)
	}
	if !got.Title.Valid || got.Title.String != "Example Page" {
		t.Errorf("expected title %q, got %v", "Example Page", got.Title)
	}
	if got.IndexabilityState != "indexable" {
		t.Errorf("expected indexability_state %q, got %q", "indexable", got.IndexabilityState)
	}
	if got.JSSuspect {
		t.Error("expected js_suspect false")
	}
	if !got.TextPreview.Valid || got.TextPreview.String != textPreview {
		t.Errorf("expected text_preview %q, got %v", textPreview, got.TextPreview)
	}
}

func TestGetPageByURL(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	urlID, err := db.UpsertURL(job.ID, "https://example.com/about", "example.com", "pending", true, "crawl")
	if err != nil {
		t.Fatalf("UpsertURL: %v", err)
	}

	fetchID, err := db.InsertFetch(FetchInput{
		JobID:          job.ID,
		FetchSeq:       1,
		RequestedURLID: urlID,
		StatusCode:     200,
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("InsertFetch: %v", err)
	}

	_, err = db.InsertPage(PageInput{
		JobID:             job.ID,
		URLID:             urlID,
		FetchID:           fetchID,
		IndexabilityState: "indexable",
	})
	if err != nil {
		t.Fatalf("InsertPage: %v", err)
	}

	got, err := db.GetPageByURL(job.ID, urlID)
	if err != nil {
		t.Fatalf("GetPageByURL: %v", err)
	}

	if got.URLID != urlID {
		t.Errorf("expected url_id %d, got %d", urlID, got.URLID)
	}
}
