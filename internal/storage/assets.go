package storage

import (
	"fmt"
)

// AssetInput holds parameters for InsertAsset.
type AssetInput struct {
	JobID           string
	URLID           int64
	ContentType     *string
	ContentEncoding *string
	StatusCode      *int
	ContentLength   *int64
}

// assetColumns is the canonical SELECT list for assets.
const assetColumns = `id, job_id, url_id, content_type, content_encoding, status_code, content_length`

// scanAsset scans a row into an Asset.
func scanAsset(sc interface{ Scan(...any) error }) (Asset, error) {
	var a Asset
	err := sc.Scan(
		&a.ID, &a.JobID, &a.URLID,
		&a.ContentType, &a.ContentEncoding, &a.StatusCode, &a.ContentLength,
	)
	return a, err
}

// InsertAsset creates a new asset record and returns its ID.
func (db *DB) InsertAsset(input AssetInput) (int64, error) {
	result, err := db.Exec(
		`INSERT INTO assets (job_id, url_id, content_type, content_encoding, status_code, content_length)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		input.JobID, input.URLID, input.ContentType, input.ContentEncoding, input.StatusCode, input.ContentLength,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting asset for URL %d in job %q: %w", input.URLID, input.JobID, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert ID for asset: %w", err)
	}

	return id, nil
}

// UpsertAssetMetadata updates existing placeholder rows for an asset URL or
// inserts a new row when the asset has not been seen before.
func (db *DB) UpsertAssetMetadata(input AssetInput) (int64, error) {
	result, err := db.Exec(
		`UPDATE assets
		 SET content_type = ?, content_encoding = ?, status_code = ?, content_length = ?
		 WHERE job_id = ? AND url_id = ?`,
		input.ContentType, input.ContentEncoding, input.StatusCode, input.ContentLength, input.JobID, input.URLID,
	)
	if err != nil {
		return 0, fmt.Errorf("updating asset for URL %d in job %q: %w", input.URLID, input.JobID, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows > 0 {
		return 0, nil
	}
	return db.InsertAsset(input)
}

// InsertAssetReference creates a new asset reference linking a page to an asset.
func (db *DB) InsertAssetReference(
	jobID string, assetURLID, sourcePageURLID int64, referenceType string,
) (int64, error) {
	result, err := db.Exec(
		`INSERT INTO asset_references (job_id, asset_url_id, source_page_url_id, reference_type)
		 VALUES (?, ?, ?, ?)`,
		jobID, assetURLID, sourcePageURLID, referenceType,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting asset reference for asset URL %d in job %q: %w",
			assetURLID, jobID, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert ID for asset reference: %w", err)
	}

	return id, nil
}

// GetAssetsByJob returns assets for a job, up to limit rows.
func (db *DB) GetAssetsByJob(jobID string, limit int) ([]Asset, error) {
	rows, err := db.Query(
		`SELECT `+assetColumns+` FROM assets
		 WHERE job_id = ? ORDER BY id ASC LIMIT ?`,
		jobID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying assets for job %q: %w", jobID, err)
	}
	defer rows.Close()

	assets := []Asset{}
	for rows.Next() {
		a, scanErr := scanAsset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning asset row: %w", scanErr)
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating asset rows: %w", err)
	}

	return assets, nil
}
