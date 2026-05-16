package storage

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// QueryFilter defines filters for paginated query methods.
type QueryFilter struct {
	IssueType        string `json:"issueType,omitempty"`
	Severity         string `json:"severity,omitempty"`         // "info", "warning", "error"
	StatusCodeFamily string `json:"statusCodeFamily,omitempty"` // "2xx", "3xx", "4xx", "5xx"
	URLPattern       string `json:"urlPattern,omitempty"`
	URLGroup         string `json:"urlGroup,omitempty"`
	MinDepth         *int   `json:"minDepth,omitempty"`
	MaxDepth         *int   `json:"maxDepth,omitempty"`
	IsInternal       *bool  `json:"isInternal,omitempty"`
	RelationType     string `json:"relationType,omitempty"`
	ContentType      string `json:"contentType,omitempty"`
	ClusterType      string `json:"clusterType,omitempty"`
	TargetDomain     string `json:"targetDomain,omitempty"`
}

// PagedResult holds a page of results with cursor info.
type PagedResult[T any] struct {
	Results        []T      `json:"results"`
	NextCursor     string   `json:"nextCursor,omitempty"`
	TotalCount     int      `json:"totalCount"`
	IgnoredFilters []string `json:"ignoredFilters,omitempty"`
}

const defaultCursorLimit = 100

func normalizeCursorLimit(limit int) int {
	if limit <= 0 {
		return defaultCursorLimit
	}
	return limit
}

// CrawlSummary aggregates crawl statistics.
type CrawlSummary struct {
	TotalPages              int            `json:"totalPages"`
	TotalURLs               int            `json:"totalUrls"`
	TotalIssues             int            `json:"totalIssues"`
	IssuesByType            map[string]int `json:"issuesByType"`
	IssuesBySeverity        map[string]int `json:"issuesBySeverity"`
	StatusCodeDistribution  map[int]int    `json:"statusCodeDistribution"`
	DepthDistribution       map[int]int    `json:"depthDistribution"`
	AvgTTFB                 float64        `json:"avgTtfb"`
	MedianTTFB              float64        `json:"medianTtfb"`
	P95TTFB                 float64        `json:"p95Ttfb"`
	AvgWordCount            float64        `json:"avgWordCount"`
	PagesWithStructuredData int            `json:"pagesWithStructuredData"`
	OrphanPageCount         int            `json:"orphanPageCount"`
	DuplicateContentCount   int            `json:"duplicateContentCount"`
	ThinContentCount        int            `json:"thinContentCount"`
	CrawlDuration           float64        `json:"crawlDuration"`
	PagesPerSecond          float64        `json:"pagesPerSecond"`
	TopIssues               []TopIssue     `json:"topIssues"`
}

// TopIssue represents a frequent issue type with an example.
type TopIssue struct {
	Type       string `json:"type"`
	Count      int    `json:"count"`
	ExampleURL string `json:"exampleUrl"`
}

// thinContentThreshold is the word count below which a page is "thin".
const thinContentThreshold = 200

// encodeCursor encodes an ID as a base64 cursor string.
func encodeCursor(id int64) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

// decodeCursor decodes a base64 cursor string to an ID.
func decodeCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("decoding cursor %q: %w", cursor, err)
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing cursor value %q: %w", string(raw), err)
	}
	return id, nil
}

// statusCodeFamilyRange returns the lower and upper bounds for a status code family.
func statusCodeFamilyRange(family string) (int, int, error) {
	switch family {
	case "2xx":
		return 200, 299, nil
	case "3xx":
		return 300, 399, nil
	case "4xx":
		return 400, 499, nil
	case "5xx":
		return 500, 599, nil
	}
	return 0, 0, fmt.Errorf("unknown status code family %q", family)
}

// collectIgnoredFilters checks which filter fields are set but not applicable.
func collectIgnoredFilters(f QueryFilter, applicable map[string]bool) []string {
	ignored := []string{}
	if f.IssueType != "" && !applicable["IssueType"] {
		ignored = append(ignored, "IssueType")
	}
	if f.Severity != "" && !applicable["Severity"] {
		ignored = append(ignored, "Severity")
	}
	if f.StatusCodeFamily != "" && !applicable["StatusCodeFamily"] {
		ignored = append(ignored, "StatusCodeFamily")
	}
	if f.URLPattern != "" && !applicable["URLPattern"] {
		ignored = append(ignored, "URLPattern")
	}
	if f.URLGroup != "" && !applicable["URLGroup"] {
		ignored = append(ignored, "URLGroup")
	}
	if f.MinDepth != nil && !applicable["MinDepth"] {
		ignored = append(ignored, "MinDepth")
	}
	if f.MaxDepth != nil && !applicable["MaxDepth"] {
		ignored = append(ignored, "MaxDepth")
	}
	if f.IsInternal != nil && !applicable["IsInternal"] {
		ignored = append(ignored, "IsInternal")
	}
	if f.RelationType != "" && !applicable["RelationType"] {
		ignored = append(ignored, "RelationType")
	}
	if f.ContentType != "" && !applicable["ContentType"] {
		ignored = append(ignored, "ContentType")
	}
	if f.ClusterType != "" && !applicable["ClusterType"] {
		ignored = append(ignored, "ClusterType")
	}
	if f.TargetDomain != "" && !applicable["TargetDomain"] {
		ignored = append(ignored, "TargetDomain")
	}
	return ignored
}

// QueryPages returns paginated pages for a job with optional filters.
func (db *DB) QueryPages(
	jobID string, filter QueryFilter, cursor string, limit int,
) (*PagedResult[Page], error) {
	limit = normalizeCursorLimit(limit)
	cursorID, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}

	applicable := map[string]bool{
		"URLPattern":       true,
		"URLGroup":         true,
		"MinDepth":         true,
		"MaxDepth":         true,
		"StatusCodeFamily": true,
		"ContentType":      true,
	}
	ignored := collectIgnoredFilters(filter, applicable)

	// We may need joins for URL pattern, status code family, or content type.
	needsURLJoin := filter.URLPattern != ""
	needsFetchJoin := filter.StatusCodeFamily != "" || filter.ContentType != ""

	var qb strings.Builder
	args := []any{}

	qb.WriteString("SELECT ")
	// Prefix page columns with p. alias
	qb.WriteString(`p.id, p.job_id, p.url_id, p.fetch_id, p.depth,
		(SELECT f.status_code FROM fetches f WHERE f.id = p.fetch_id),
		(SELECT f.content_type FROM fetches f WHERE f.id = p.fetch_id),
		p.title, p.title_length, p.meta_description, p.meta_description_length,
		p.meta_robots, p.x_robots_tag, p.indexability_state,
		p.canonical_url, p.canonical_is_self, p.canonical_status_code,
		p.rel_next_url, p.rel_prev_url, p.hreflang_json,
		p.h1_json, p.h2_json, p.h3_json, p.h4_json, p.h5_json, p.h6_json,
		p.og_title, p.og_description, p.og_image, p.og_url, p.og_type,
		p.twitter_card, p.twitter_title, p.twitter_description, p.twitter_image,
		p.jsonld_raw, p.jsonld_types_json, p.images_json,
		p.word_count, p.main_content_word_count, p.content_hash,
		p.text_preview,
		p.js_suspect, p.url_group, p.outbound_edge_count, p.inbound_edge_count,
		p.inbound_linking_pages`)

	qb.WriteString(" FROM pages p")

	if needsURLJoin {
		qb.WriteString(" JOIN urls u ON u.id = p.url_id")
	}
	if needsFetchJoin {
		qb.WriteString(" JOIN fetches f ON f.id = p.fetch_id")
	}

	qb.WriteString(" WHERE p.job_id = ? AND p.id > ?")
	args = append(args, jobID, cursorID)

	if filter.URLPattern != "" {
		qb.WriteString(" AND u.normalized_url LIKE ?")
		args = append(args, "%"+filter.URLPattern+"%")
	}
	if filter.URLGroup != "" {
		qb.WriteString(" AND p.url_group = ?")
		args = append(args, filter.URLGroup)
	}
	if filter.MinDepth != nil {
		qb.WriteString(" AND p.depth >= ?")
		args = append(args, *filter.MinDepth)
	}
	if filter.MaxDepth != nil {
		qb.WriteString(" AND p.depth <= ?")
		args = append(args, *filter.MaxDepth)
	}
	if filter.StatusCodeFamily != "" {
		lo, hi, scErr := statusCodeFamilyRange(filter.StatusCodeFamily)
		if scErr != nil {
			return nil, scErr
		}
		qb.WriteString(" AND f.status_code >= ? AND f.status_code <= ?")
		args = append(args, lo, hi)
	}
	if filter.ContentType != "" {
		qb.WriteString(" AND f.content_type LIKE ?")
		args = append(args, "%"+filter.ContentType+"%")
	}

	qb.WriteString(" ORDER BY p.id ASC LIMIT ?")
	args = append(args, limit+1)

	rows, err := db.Query(qb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("querying pages for job %q: %w", jobID, err)
	}
	defer rows.Close()

	pages := []Page{}
	for rows.Next() {
		p, scanErr := scanPage(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning page row: %w", scanErr)
		}
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating page rows: %w", err)
	}

	result := &PagedResult[Page]{
		Results:        []Page{},
		IgnoredFilters: ignored,
	}

	if len(pages) > limit {
		result.Results = pages[:limit]
		result.NextCursor = encodeCursor(pages[limit-1].ID)
	} else {
		result.Results = pages
	}

	// Count total
	var countQB strings.Builder
	countArgs := []any{}
	countQB.WriteString("SELECT COUNT(*) FROM pages p")
	if needsURLJoin {
		countQB.WriteString(" JOIN urls u ON u.id = p.url_id")
	}
	if needsFetchJoin {
		countQB.WriteString(" JOIN fetches f ON f.id = p.fetch_id")
	}
	countQB.WriteString(" WHERE p.job_id = ?")
	countArgs = append(countArgs, jobID)

	if filter.URLPattern != "" {
		countQB.WriteString(" AND u.normalized_url LIKE ?")
		countArgs = append(countArgs, "%"+filter.URLPattern+"%")
	}
	if filter.URLGroup != "" {
		countQB.WriteString(" AND p.url_group = ?")
		countArgs = append(countArgs, filter.URLGroup)
	}
	if filter.MinDepth != nil {
		countQB.WriteString(" AND p.depth >= ?")
		countArgs = append(countArgs, *filter.MinDepth)
	}
	if filter.MaxDepth != nil {
		countQB.WriteString(" AND p.depth <= ?")
		countArgs = append(countArgs, *filter.MaxDepth)
	}
	if filter.StatusCodeFamily != "" {
		lo, hi, _ := statusCodeFamilyRange(filter.StatusCodeFamily)
		countQB.WriteString(" AND f.status_code >= ? AND f.status_code <= ?")
		countArgs = append(countArgs, lo, hi)
	}
	if filter.ContentType != "" {
		countQB.WriteString(" AND f.content_type LIKE ?")
		countArgs = append(countArgs, "%"+filter.ContentType+"%")
	}

	err = db.QueryRow(countQB.String(), countArgs...).Scan(&result.TotalCount)
	if err != nil {
		return nil, fmt.Errorf("counting pages for job %q: %w", jobID, err)
	}

	return result, nil
}

// QueryIssues returns paginated issues for a job with optional filters.
func (db *DB) QueryIssues(
	jobID string, filter QueryFilter, cursor string, limit int,
) (*PagedResult[Issue], error) {
	limit = normalizeCursorLimit(limit)
	cursorID, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}

	applicable := map[string]bool{
		"IssueType":  true,
		"Severity":   true,
		"URLPattern": true,
	}
	ignored := collectIgnoredFilters(filter, applicable)

	needsURLJoin := filter.URLPattern != ""

	var qb strings.Builder
	args := []any{}

	qb.WriteString("SELECT i.id, i.job_id, i.url_id, i.issue_type, i.severity, i.scope, i.details_json")
	qb.WriteString(" FROM issues i")
	if needsURLJoin {
		qb.WriteString(" JOIN urls u ON u.id = i.url_id")
	}
	qb.WriteString(" WHERE i.job_id = ? AND i.id > ?")
	args = append(args, jobID, cursorID)

	if filter.IssueType != "" {
		qb.WriteString(" AND i.issue_type = ?")
		args = append(args, filter.IssueType)
	}
	if filter.Severity != "" {
		qb.WriteString(" AND i.severity = ?")
		args = append(args, filter.Severity)
	}
	if filter.URLPattern != "" {
		qb.WriteString(" AND u.normalized_url LIKE ?")
		args = append(args, "%"+filter.URLPattern+"%")
	}

	qb.WriteString(" ORDER BY i.id ASC LIMIT ?")
	args = append(args, limit+1)

	rows, err := db.Query(qb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("querying issues for job %q: %w", jobID, err)
	}
	defer rows.Close()

	issues := []Issue{}
	for rows.Next() {
		issue, scanErr := scanIssue(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning issue row: %w", scanErr)
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue rows: %w", err)
	}

	result := &PagedResult[Issue]{
		Results:        []Issue{},
		IgnoredFilters: ignored,
	}

	if len(issues) > limit {
		result.Results = issues[:limit]
		result.NextCursor = encodeCursor(issues[limit-1].ID)
	} else {
		result.Results = issues
	}

	// Count total
	var countQB strings.Builder
	countArgs := []any{}
	countQB.WriteString("SELECT COUNT(*) FROM issues i")
	if needsURLJoin {
		countQB.WriteString(" JOIN urls u ON u.id = i.url_id")
	}
	countQB.WriteString(" WHERE i.job_id = ?")
	countArgs = append(countArgs, jobID)
	if filter.IssueType != "" {
		countQB.WriteString(" AND i.issue_type = ?")
		countArgs = append(countArgs, filter.IssueType)
	}
	if filter.Severity != "" {
		countQB.WriteString(" AND i.severity = ?")
		countArgs = append(countArgs, filter.Severity)
	}
	if filter.URLPattern != "" {
		countQB.WriteString(" AND u.normalized_url LIKE ?")
		countArgs = append(countArgs, "%"+filter.URLPattern+"%")
	}

	err = db.QueryRow(countQB.String(), countArgs...).Scan(&result.TotalCount)
	if err != nil {
		return nil, fmt.Errorf("counting issues for job %q: %w", jobID, err)
	}

	return result, nil
}

// QueryEdgesView returns paginated edges for a job with optional filters.
func (db *DB) QueryEdgesView(
	jobID string, filter QueryFilter, cursor string, limit int,
) (*PagedResult[Edge], error) {
	limit = normalizeCursorLimit(limit)
	cursorID, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}

	applicable := map[string]bool{
		"RelationType": true,
		"IsInternal":   true,
		"TargetDomain": true,
	}
	ignored := collectIgnoredFilters(filter, applicable)

	needsURLJoin := filter.TargetDomain != ""

	var qb strings.Builder
	args := []any{}

	qb.WriteString("SELECT e.id, e.job_id, e.source_url_id, e.normalized_target_url_id,")
	qb.WriteString(" e.source_kind, e.relation_type, e.rel_flags_json, e.discovery_mode,")
	qb.WriteString(" e.anchor_text, e.is_internal, e.declared_target_url,")
	qb.WriteString(" e.final_target_url_id, e.target_status_code")
	qb.WriteString(" FROM edges e")
	if needsURLJoin {
		qb.WriteString(" JOIN urls tu ON tu.id = e.normalized_target_url_id")
	}
	qb.WriteString(" WHERE e.job_id = ? AND e.id > ?")
	args = append(args, jobID, cursorID)

	if filter.RelationType != "" {
		qb.WriteString(" AND e.relation_type = ?")
		args = append(args, filter.RelationType)
	}
	if filter.IsInternal != nil {
		qb.WriteString(" AND e.is_internal = ?")
		args = append(args, boolToInt(*filter.IsInternal))
	}
	if filter.TargetDomain != "" {
		qb.WriteString(" AND tu.host = ?")
		args = append(args, filter.TargetDomain)
	}

	qb.WriteString(" ORDER BY e.id ASC LIMIT ?")
	args = append(args, limit+1)

	rows, err := db.Query(qb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("querying edges for job %q: %w", jobID, err)
	}
	defer rows.Close()

	edges := []Edge{}
	for rows.Next() {
		e, scanErr := scanEdge(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning edge row: %w", scanErr)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating edge rows: %w", err)
	}

	result := &PagedResult[Edge]{
		Results:        []Edge{},
		IgnoredFilters: ignored,
	}

	if len(edges) > limit {
		result.Results = edges[:limit]
		result.NextCursor = encodeCursor(edges[limit-1].ID)
	} else {
		result.Results = edges
	}

	// Count total
	var countQB strings.Builder
	countArgs := []any{}
	countQB.WriteString("SELECT COUNT(*) FROM edges e")
	if needsURLJoin {
		countQB.WriteString(" JOIN urls tu ON tu.id = e.normalized_target_url_id")
	}
	countQB.WriteString(" WHERE e.job_id = ?")
	countArgs = append(countArgs, jobID)
	if filter.RelationType != "" {
		countQB.WriteString(" AND e.relation_type = ?")
		countArgs = append(countArgs, filter.RelationType)
	}
	if filter.IsInternal != nil {
		countQB.WriteString(" AND e.is_internal = ?")
		countArgs = append(countArgs, boolToInt(*filter.IsInternal))
	}
	if filter.TargetDomain != "" {
		countQB.WriteString(" AND tu.host = ?")
		countArgs = append(countArgs, filter.TargetDomain)
	}

	err = db.QueryRow(countQB.String(), countArgs...).Scan(&result.TotalCount)
	if err != nil {
		return nil, fmt.Errorf("counting edges for job %q: %w", jobID, err)
	}

	return result, nil
}

// QueryResponseCodes returns paginated fetches filtered by status code family.
func (db *DB) QueryResponseCodes(
	jobID string, filter QueryFilter, cursor string, limit int,
) (*PagedResult[Fetch], error) {
	limit = normalizeCursorLimit(limit)
	cursorID, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}

	applicable := map[string]bool{
		"StatusCodeFamily": true,
	}
	ignored := collectIgnoredFilters(filter, applicable)

	var qb strings.Builder
	args := []any{}

	qb.WriteString("SELECT ")
	qb.WriteString(fetchColumns)
	qb.WriteString(" FROM fetches WHERE job_id = ? AND id > ?")
	args = append(args, jobID, cursorID)

	if filter.StatusCodeFamily != "" {
		lo, hi, scErr := statusCodeFamilyRange(filter.StatusCodeFamily)
		if scErr != nil {
			return nil, scErr
		}
		qb.WriteString(" AND status_code >= ? AND status_code <= ?")
		args = append(args, lo, hi)
	}

	qb.WriteString(" ORDER BY id ASC LIMIT ?")
	args = append(args, limit+1)

	rows, err := db.Query(qb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("querying fetches for job %q: %w", jobID, err)
	}
	defer rows.Close()

	fetches := []Fetch{}
	for rows.Next() {
		f, scanErr := scanFetch(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning fetch row: %w", scanErr)
		}
		fetches = append(fetches, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating fetch rows: %w", err)
	}

	result := &PagedResult[Fetch]{
		Results:        []Fetch{},
		IgnoredFilters: ignored,
	}

	if len(fetches) > limit {
		result.Results = fetches[:limit]
		result.NextCursor = encodeCursor(fetches[limit-1].ID)
	} else {
		result.Results = fetches
	}

	// Count total
	var countQB strings.Builder
	countArgs := []any{}
	countQB.WriteString("SELECT COUNT(*) FROM fetches WHERE job_id = ?")
	countArgs = append(countArgs, jobID)
	if filter.StatusCodeFamily != "" {
		lo, hi, _ := statusCodeFamilyRange(filter.StatusCodeFamily)
		countQB.WriteString(" AND status_code >= ? AND status_code <= ?")
		countArgs = append(countArgs, lo, hi)
	}

	err = db.QueryRow(countQB.String(), countArgs...).Scan(&result.TotalCount)
	if err != nil {
		return nil, fmt.Errorf("counting fetches for job %q: %w", jobID, err)
	}

	return result, nil
}

// GetCrawlSummary computes aggregate statistics for a crawl job.
func (db *DB) GetCrawlSummary(jobID string) (*CrawlSummary, error) {
	s := &CrawlSummary{
		IssuesByType:           map[string]int{},
		IssuesBySeverity:       map[string]int{},
		StatusCodeDistribution: map[int]int{},
		DepthDistribution:      map[int]int{},
		TopIssues:              []TopIssue{},
	}

	// Total pages
	err := db.QueryRow("SELECT COUNT(*) FROM pages WHERE job_id = ?", jobID).Scan(&s.TotalPages)
	if err != nil {
		return nil, fmt.Errorf("counting pages for job %q: %w", jobID, err)
	}

	// Total URLs
	err = db.QueryRow("SELECT COUNT(*) FROM urls WHERE job_id = ?", jobID).Scan(&s.TotalURLs)
	if err != nil {
		return nil, fmt.Errorf("counting urls for job %q: %w", jobID, err)
	}

	// Total issues
	err = db.QueryRow("SELECT COUNT(*) FROM issues WHERE job_id = ?", jobID).Scan(&s.TotalIssues)
	if err != nil {
		return nil, fmt.Errorf("counting issues for job %q: %w", jobID, err)
	}

	// Issues by type
	rows, err := db.Query(
		"SELECT issue_type, COUNT(*) FROM issues WHERE job_id = ? GROUP BY issue_type", jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying issues by type for job %q: %w", jobID, err)
	}
	for rows.Next() {
		var t string
		var c int
		if scanErr := rows.Scan(&t, &c); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning issue type: %w", scanErr)
		}
		s.IssuesByType[t] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue types: %w", err)
	}
	rows.Close()

	// Issues by severity
	rows, err = db.Query(
		"SELECT severity, COUNT(*) FROM issues WHERE job_id = ? GROUP BY severity", jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying issues by severity for job %q: %w", jobID, err)
	}
	for rows.Next() {
		var sev string
		var c int
		if scanErr := rows.Scan(&sev, &c); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning issue severity: %w", scanErr)
		}
		s.IssuesBySeverity[sev] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue severities: %w", err)
	}
	rows.Close()

	// Status code distribution
	rows, err = db.Query(
		"SELECT status_code, COUNT(*) FROM fetches WHERE job_id = ? AND status_code IS NOT NULL GROUP BY status_code",
		jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying status codes for job %q: %w", jobID, err)
	}
	for rows.Next() {
		var code, c int
		if scanErr := rows.Scan(&code, &c); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning status code: %w", scanErr)
		}
		s.StatusCodeDistribution[code] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating status codes: %w", err)
	}
	rows.Close()

	// Depth distribution
	rows, err = db.Query(
		"SELECT depth, COUNT(*) FROM pages WHERE job_id = ? GROUP BY depth", jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying depth distribution for job %q: %w", jobID, err)
	}
	for rows.Next() {
		var depth, c int
		if scanErr := rows.Scan(&depth, &c); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning depth: %w", scanErr)
		}
		s.DepthDistribution[depth] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating depth distribution: %w", err)
	}
	rows.Close()

	// TTFB stats: avg, median, p95
	var ttfbCount int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM fetches WHERE job_id = ? AND ttfb_ms IS NOT NULL", jobID,
	).Scan(&ttfbCount)
	if err != nil {
		return nil, fmt.Errorf("counting ttfb for job %q: %w", jobID, err)
	}

	if ttfbCount > 0 {
		err = db.QueryRow(
			"SELECT AVG(ttfb_ms) FROM fetches WHERE job_id = ? AND ttfb_ms IS NOT NULL", jobID,
		).Scan(&s.AvgTTFB)
		if err != nil {
			return nil, fmt.Errorf("computing avg ttfb for job %q: %w", jobID, err)
		}

		// Median: offset at count/2
		medianOffset := ttfbCount / 2
		var medianVal int64
		err = db.QueryRow(
			"SELECT ttfb_ms FROM fetches WHERE job_id = ? AND ttfb_ms IS NOT NULL ORDER BY ttfb_ms ASC LIMIT 1 OFFSET ?",
			jobID, medianOffset,
		).Scan(&medianVal)
		if err != nil {
			return nil, fmt.Errorf("computing median ttfb for job %q: %w", jobID, err)
		}
		s.MedianTTFB = float64(medianVal)

		// P95: offset at count * 0.95
		p95Offset := int(float64(ttfbCount) * 0.95)
		if p95Offset >= ttfbCount {
			p95Offset = ttfbCount - 1
		}
		var p95Val int64
		err = db.QueryRow(
			"SELECT ttfb_ms FROM fetches WHERE job_id = ? AND ttfb_ms IS NOT NULL ORDER BY ttfb_ms ASC LIMIT 1 OFFSET ?",
			jobID, p95Offset,
		).Scan(&p95Val)
		if err != nil {
			return nil, fmt.Errorf("computing p95 ttfb for job %q: %w", jobID, err)
		}
		s.P95TTFB = float64(p95Val)
	}

	// Avg word count
	err = db.QueryRow(
		"SELECT COALESCE(AVG(word_count), 0) FROM pages WHERE job_id = ? AND word_count IS NOT NULL",
		jobID,
	).Scan(&s.AvgWordCount)
	if err != nil {
		return nil, fmt.Errorf("computing avg word count for job %q: %w", jobID, err)
	}

	// Structured data count
	err = db.QueryRow(
		"SELECT COUNT(*) FROM pages WHERE job_id = ? AND jsonld_raw IS NOT NULL AND jsonld_raw != '[]'",
		jobID,
	).Scan(&s.PagesWithStructuredData)
	if err != nil {
		return nil, fmt.Errorf("counting structured data for job %q: %w", jobID, err)
	}

	// Orphan pages (inbound_edge_count = 0)
	err = db.QueryRow(
		"SELECT COUNT(*) FROM pages WHERE job_id = ? AND inbound_edge_count = 0",
		jobID,
	).Scan(&s.OrphanPageCount)
	if err != nil {
		return nil, fmt.Errorf("counting orphan pages for job %q: %w", jobID, err)
	}

	// Duplicate content count (from duplicate_clusters)
	err = db.QueryRow(
		"SELECT COALESCE(SUM(member_count), 0) FROM duplicate_clusters WHERE job_id = ?",
		jobID,
	).Scan(&s.DuplicateContentCount)
	if err != nil {
		return nil, fmt.Errorf("counting duplicates for job %q: %w", jobID, err)
	}

	// Thin content count
	err = db.QueryRow(
		"SELECT COUNT(*) FROM pages WHERE job_id = ? AND word_count IS NOT NULL AND word_count < ?",
		jobID, thinContentThreshold,
	).Scan(&s.ThinContentCount)
	if err != nil {
		return nil, fmt.Errorf("counting thin content for job %q: %w", jobID, err)
	}

	// Crawl duration and pages/sec
	var startedAt, finishedAt *string
	err = db.QueryRow(
		"SELECT started_at, finished_at FROM crawl_jobs WHERE id = ?", jobID,
	).Scan(&startedAt, &finishedAt)
	if err != nil {
		return nil, fmt.Errorf("getting job times for %q: %w", jobID, err)
	}
	if startedAt != nil && finishedAt != nil {
		// Use SQLite to compute duration in seconds
		var duration float64
		err = db.QueryRow(
			"SELECT (julianday(?) - julianday(?)) * 86400.0", *finishedAt, *startedAt,
		).Scan(&duration)
		if err != nil {
			return nil, fmt.Errorf("computing duration for job %q: %w", jobID, err)
		}
		s.CrawlDuration = duration
		if duration > 0 {
			s.PagesPerSecond = float64(s.TotalPages) / duration
		}
	}

	// Top issues: top 10 by count with example URL
	rows, err = db.Query(`
		SELECT i.issue_type, COUNT(*) as cnt, COALESCE(
			(SELECT u.normalized_url FROM urls u
			 WHERE u.id = i.url_id LIMIT 1), ''
		) as example_url
		FROM issues i
		WHERE i.job_id = ?
		GROUP BY i.issue_type
		ORDER BY cnt DESC
		LIMIT 10`, jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying top issues for job %q: %w", jobID, err)
	}
	for rows.Next() {
		var ti TopIssue
		if scanErr := rows.Scan(&ti.Type, &ti.Count, &ti.ExampleURL); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning top issue: %w", scanErr)
		}
		s.TopIssues = append(s.TopIssues, ti)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating top issues: %w", err)
	}
	rows.Close()

	return s, nil
}
