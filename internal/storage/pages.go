package storage

import (
	"database/sql"
	"fmt"
)

// PageInput holds parameters for InsertPage.
type PageInput struct {
	JobID                 string
	URLID                 int64
	FetchID               int64
	Depth                 int
	Title                 *string
	TitleLength           *int
	MetaDescription       *string
	MetaDescriptionLength *int
	MetaRobots            *string
	XRobotsTag            *string
	IndexabilityState     string
	CanonicalURL          *string
	CanonicalIsSelf       *int
	CanonicalStatusCode   *int
	RelNextURL            *string
	RelPrevURL            *string
	HreflangJSON          *string
	H1JSON                *string
	H2JSON                *string
	H3JSON                *string
	H4JSON                *string
	H5JSON                *string
	H6JSON                *string
	OGTitle               *string
	OGDescription         *string
	OGImage               *string
	OGURL                 *string
	OGType                *string
	TwitterCard           *string
	TwitterTitle          *string
	TwitterDescription    *string
	TwitterImage          *string
	JSONLDRaw             *string
	JSONLDTypesJSON       *string
	ImagesJSON            *string
	WordCount             *int
	MainContentWordCount  *int
	ContentHash           *string
	TextPreview           *string
	JSSuspect             bool
	URLGroup              *string
}

// pageColumns is the canonical SELECT list for pages.
const pageColumns = `id, job_id, url_id, fetch_id, depth,
	title, title_length, meta_description, meta_description_length,
	meta_robots, x_robots_tag, indexability_state,
	canonical_url, canonical_is_self, canonical_status_code,
	rel_next_url, rel_prev_url, hreflang_json,
	h1_json, h2_json, h3_json, h4_json, h5_json, h6_json,
	og_title, og_description, og_image, og_url, og_type,
	twitter_card, twitter_title, twitter_description, twitter_image,
	jsonld_raw, jsonld_types_json, images_json,
	word_count, main_content_word_count, content_hash,
	text_preview,
	js_suspect, url_group, outbound_edge_count, inbound_edge_count,
	inbound_linking_pages`

// scanPage scans a row into a Page using the pageColumns order.
func scanPage(sc interface{ Scan(...any) error }) (Page, error) {
	var p Page
	var jsSuspect int
	err := sc.Scan(
		&p.ID, &p.JobID, &p.URLID, &p.FetchID, &p.Depth,
		&p.Title, &p.TitleLength, &p.MetaDescription, &p.MetaDescriptionLength,
		&p.MetaRobots, &p.XRobotsTag, &p.IndexabilityState,
		&p.CanonicalURL, &p.CanonicalIsSelf, &p.CanonicalStatusCode,
		&p.RelNextURL, &p.RelPrevURL, &p.HreflangJSON,
		&p.H1JSON, &p.H2JSON, &p.H3JSON, &p.H4JSON, &p.H5JSON, &p.H6JSON,
		&p.OGTitle, &p.OGDescription, &p.OGImage, &p.OGURL, &p.OGType,
		&p.TwitterCard, &p.TwitterTitle, &p.TwitterDescription, &p.TwitterImage,
		&p.JSONLDRaw, &p.JSONLDTypesJSON, &p.ImagesJSON,
		&p.WordCount, &p.MainContentWordCount, &p.ContentHash,
		&p.TextPreview,
		&jsSuspect, &p.URLGroup, &p.OutboundEdgeCount, &p.InboundEdgeCount,
		&p.InboundLinkingPages,
	)
	p.JSSuspect = jsSuspect == 1
	return p, err
}

// InsertPage creates a new page record and returns its ID.
func (db *DB) InsertPage(input PageInput) (int64, error) {
	result, err := db.Exec(
		`INSERT INTO pages (job_id, url_id, fetch_id, depth,
			title, title_length, meta_description, meta_description_length,
			meta_robots, x_robots_tag, indexability_state,
			canonical_url, canonical_is_self, canonical_status_code,
			rel_next_url, rel_prev_url, hreflang_json,
			h1_json, h2_json, h3_json, h4_json, h5_json, h6_json,
			og_title, og_description, og_image, og_url, og_type,
			twitter_card, twitter_title, twitter_description, twitter_image,
			jsonld_raw, jsonld_types_json, images_json,
			word_count, main_content_word_count, content_hash,
			text_preview,
			js_suspect, url_group)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.JobID, input.URLID, input.FetchID, input.Depth,
		input.Title, input.TitleLength, input.MetaDescription, input.MetaDescriptionLength,
		input.MetaRobots, input.XRobotsTag, input.IndexabilityState,
		input.CanonicalURL, input.CanonicalIsSelf, input.CanonicalStatusCode,
		input.RelNextURL, input.RelPrevURL, input.HreflangJSON,
		input.H1JSON, input.H2JSON, input.H3JSON, input.H4JSON, input.H5JSON, input.H6JSON,
		input.OGTitle, input.OGDescription, input.OGImage, input.OGURL, input.OGType,
		input.TwitterCard, input.TwitterTitle, input.TwitterDescription, input.TwitterImage,
		input.JSONLDRaw, input.JSONLDTypesJSON, input.ImagesJSON,
		input.WordCount, input.MainContentWordCount, input.ContentHash,
		input.TextPreview,
		boolToInt(input.JSSuspect), input.URLGroup,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting page for URL %d in job %q: %w", input.URLID, input.JobID, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert ID for page: %w", err)
	}

	return id, nil
}

// GetPage retrieves a page by its auto-increment ID.
func (db *DB) GetPage(id int64) (*Page, error) {
	row := db.QueryRow(
		`SELECT `+pageColumns+` FROM pages WHERE id = ?`, id,
	)

	p, err := scanPage(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("page %d not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("scanning page %d: %w", id, err)
	}

	return &p, nil
}

// GetPageByURL retrieves a page by job ID and URL ID.
func (db *DB) GetPageByURL(jobID string, urlID int64) (*Page, error) {
	row := db.QueryRow(
		`SELECT `+pageColumns+` FROM pages WHERE job_id = ? AND url_id = ?`,
		jobID, urlID,
	)

	p, err := scanPage(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("page for URL %d in job %q not found", urlID, jobID)
	}
	if err != nil {
		return nil, fmt.Errorf("scanning page for URL %d in job %q: %w", urlID, jobID, err)
	}

	return &p, nil
}
