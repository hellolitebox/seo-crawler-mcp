package storage

import "testing"

func TestInsertAndGetAssets(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	urlID, err := db.UpsertURL(job.ID, "https://example.com/style.css", "example.com", "pending", true, "crawl")
	if err != nil {
		t.Fatalf("UpsertURL: %v", err)
	}

	ct := "text/css"
	sc := 200
	cl := int64(12345)
	id, err := db.InsertAsset(AssetInput{
		JobID:         job.ID,
		URLID:         urlID,
		ContentType:   &ct,
		StatusCode:    &sc,
		ContentLength: &cl,
	})
	if err != nil {
		t.Fatalf("InsertAsset: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero asset ID")
	}

	assets, err := db.GetAssetsByJob(job.ID, 1000)
	if err != nil {
		t.Fatalf("GetAssetsByJob: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(assets))
	}
	if !assets[0].ContentType.Valid || assets[0].ContentType.String != "text/css" {
		t.Errorf("expected content_type %q, got %v", "text/css", assets[0].ContentType)
	}
}

func TestUpsertAssetMetadataUpdatesPlaceholder(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	urlID, err := db.UpsertURL(job.ID, "https://example.com/_next/image?url=%2Fhero.webp&w=3840&q=75", "example.com", "pending", true, "asset")
	if err != nil {
		t.Fatalf("UpsertURL: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO assets (job_id, url_id) VALUES (?, ?)`, job.ID, urlID); err != nil {
		t.Fatalf("insert placeholder asset: %v", err)
	}

	ct := "image/jpeg"
	sc := 200
	cl := int64(260588)
	if _, err := db.UpsertAssetMetadata(AssetInput{
		JobID:         job.ID,
		URLID:         urlID,
		ContentType:   &ct,
		StatusCode:    &sc,
		ContentLength: &cl,
	}); err != nil {
		t.Fatalf("UpsertAssetMetadata: %v", err)
	}

	assets, err := db.GetAssetsByJob(job.ID, 1000)
	if err != nil {
		t.Fatalf("GetAssetsByJob: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("expected placeholder to be updated in place, got %d assets", len(assets))
	}
	if !assets[0].ContentType.Valid || assets[0].ContentType.String != ct {
		t.Fatalf("expected content_type %q, got %v", ct, assets[0].ContentType)
	}
	if !assets[0].StatusCode.Valid || assets[0].StatusCode.Int64 != int64(sc) {
		t.Fatalf("expected status_code %d, got %v", sc, assets[0].StatusCode)
	}
	if !assets[0].ContentLength.Valid || assets[0].ContentLength.Int64 != cl {
		t.Fatalf("expected content_length %d, got %v", cl, assets[0].ContentLength)
	}
}

func TestInsertAssetReference(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	assetURLID, err := db.UpsertURL(job.ID, "https://example.com/app.js", "example.com", "pending", true, "crawl")
	if err != nil {
		t.Fatalf("UpsertURL asset: %v", err)
	}

	pageURLID, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "pending", true, "seed")
	if err != nil {
		t.Fatalf("UpsertURL page: %v", err)
	}

	id, err := db.InsertAssetReference(job.ID, assetURLID, pageURLID, "script")
	if err != nil {
		t.Fatalf("InsertAssetReference: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero asset reference ID")
	}
}
