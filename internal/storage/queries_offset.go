package storage

import (
	"fmt"
	"strings"
)

// QueryPagesOffset returns pages for a job using LIMIT/OFFSET pagination.
// Unlike QueryPages (cursor-based), this supports random-access pagination for HTTP API use.
func (db *DB) QueryPagesOffset(
	jobID string, filter QueryFilter, limit, offset int,
) (*PagedResult[Page], error) {
	needsURLJoin := filter.URLPattern != ""
	needsFetchJoin := filter.StatusCodeFamily != "" || filter.ContentType != ""

	var filterClause strings.Builder
	filterArgs := []any{}

	if filter.URLPattern != "" {
		filterClause.WriteString(" AND u.normalized_url LIKE ?")
		filterArgs = append(filterArgs, "%"+filter.URLPattern+"%")
	}
	if filter.URLGroup != "" {
		filterClause.WriteString(" AND p.url_group = ?")
		filterArgs = append(filterArgs, filter.URLGroup)
	}
	if filter.MinDepth != nil {
		filterClause.WriteString(" AND p.depth >= ?")
		filterArgs = append(filterArgs, *filter.MinDepth)
	}
	if filter.MaxDepth != nil {
		filterClause.WriteString(" AND p.depth <= ?")
		filterArgs = append(filterArgs, *filter.MaxDepth)
	}
	if filter.StatusCodeFamily != "" {
		lo, hi, err := statusCodeFamilyRange(filter.StatusCodeFamily)
		if err != nil {
			return nil, err
		}
		filterClause.WriteString(" AND f.status_code >= ? AND f.status_code <= ?")
		filterArgs = append(filterArgs, lo, hi)
	}
	if filter.ContentType != "" {
		filterClause.WriteString(" AND f.content_type LIKE ?")
		filterArgs = append(filterArgs, "%"+filter.ContentType+"%")
	}

	joins := ""
	if needsURLJoin {
		joins += " JOIN urls u ON u.id = p.url_id"
	}
	if needsFetchJoin {
		joins += " JOIN fetches f ON f.id = p.fetch_id"
	}

	countArgs := append([]any{jobID}, filterArgs...)
	countSQL := "SELECT COUNT(*) FROM pages p" + joins + " WHERE p.job_id = ?" + filterClause.String()
	var totalCount int
	if err := db.QueryRow(countSQL, countArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("counting pages for job %q: %w", jobID, err)
	}

	const selectCols = `p.id, p.job_id, p.url_id, p.fetch_id, p.depth,
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
		p.inbound_linking_pages`

	selectSQL := "SELECT " + selectCols + " FROM pages p" + joins +
		" WHERE p.job_id = ?" + filterClause.String() +
		" ORDER BY p.id ASC LIMIT ? OFFSET ?"

	queryArgs := append(append([]any{jobID}, filterArgs...), limit, offset)
	rows, err := db.Query(selectSQL, queryArgs...)
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

	return &PagedResult[Page]{
		Results:    pages,
		TotalCount: totalCount,
	}, nil
}

// QueryIssuesOffset returns issues for a job using LIMIT/OFFSET pagination.
// Supports filtering by IssueType, Severity, and URLPattern.
func (db *DB) QueryIssuesOffset(
	jobID string, filter QueryFilter, limit, offset int,
) (*PagedResult[Issue], error) {
	needsURLJoin := filter.URLPattern != ""

	var filterClause strings.Builder
	filterArgs := []any{}

	if filter.IssueType != "" {
		filterClause.WriteString(" AND i.issue_type = ?")
		filterArgs = append(filterArgs, filter.IssueType)
	}
	if filter.Severity != "" {
		filterClause.WriteString(" AND i.severity = ?")
		filterArgs = append(filterArgs, filter.Severity)
	}
	if filter.URLPattern != "" {
		filterClause.WriteString(" AND u.normalized_url LIKE ?")
		filterArgs = append(filterArgs, "%"+filter.URLPattern+"%")
	}

	joins := ""
	if needsURLJoin {
		joins = " JOIN urls u ON u.id = i.url_id"
	}

	countArgs := append([]any{jobID}, filterArgs...)
	countSQL := "SELECT COUNT(*) FROM issues i" + joins + " WHERE i.job_id = ?" + filterClause.String()
	var totalCount int
	if err := db.QueryRow(countSQL, countArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("counting issues for job %q: %w", jobID, err)
	}

	selectSQL := "SELECT i.id, i.job_id, i.url_id, i.issue_type, i.severity, i.scope, i.details_json" +
		" FROM issues i" + joins +
		" WHERE i.job_id = ?" + filterClause.String() +
		" ORDER BY i.id ASC LIMIT ? OFFSET ?"

	queryArgs := append(append([]any{jobID}, filterArgs...), limit, offset)
	rows, err := db.Query(selectSQL, queryArgs...)
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

	return &PagedResult[Issue]{
		Results:    issues,
		TotalCount: totalCount,
	}, nil
}
