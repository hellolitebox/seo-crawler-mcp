package storage

import (
	"fmt"
	"strings"
)

func normalizeSortDirection(dir string) string {
	if strings.EqualFold(dir, "desc") {
		return "DESC"
	}
	return "ASC"
}

func orderClause(sortBy, sortDir string, allowed map[string]string, fallback string) string {
	key := strings.ToLower(strings.TrimSpace(sortBy))
	expr := allowed[key]
	if expr == "" {
		expr = fallback
	}
	return " ORDER BY " + expr + " " + normalizeSortDirection(sortDir)
}

func prefixedSelectColumns(prefix, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

// QueryPagesOffset returns pages for a job using LIMIT/OFFSET pagination.
// Unlike QueryPages (cursor-based), this supports random-access pagination for HTTP API use.
func (db *DB) QueryPagesOffset(
	jobID string, filter QueryFilter, limit, offset int,
) (*PagedResult[Page], error) {
	sortBy := strings.ToLower(strings.TrimSpace(filter.SortBy))
	needsURLJoin := filter.URLPattern != "" || sortBy == "url" || sortBy == "sitemap"
	needsFetchJoin := filter.StatusCodeFamily != "" || filter.ContentType != "" || sortBy == "status"

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
	if filter.Indexability != "" {
		filterClause.WriteString(" AND p.indexability_state = ?")
		filterArgs = append(filterArgs, filter.Indexability)
	}
	if filter.IssuePresence == "has_issues" {
		filterClause.WriteString(" AND EXISTS (SELECT 1 FROM issues i WHERE i.job_id = p.job_id AND i.url_id = p.url_id)")
	}
	if filter.IssuePresence == "no_issues" {
		filterClause.WriteString(" AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.job_id = p.job_id AND i.url_id = p.url_id)")
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
		p.inbound_linking_pages`
	sitemapMatchSort := "EXISTS (SELECT 1 FROM sitemap_entries se WHERE se.job_id = p.job_id AND " +
		SitemapComparableURLSQL("se.url") + " = " + SitemapComparableURLSQL("u.normalized_url") + ")"

	pageSorts := map[string]string{
		"url":      "u.normalized_url",
		"title":    "p.title",
		"status":   "f.status_code",
		"sitemap":  sitemapMatchSort,
		"depth":    "p.depth",
		"words":    "p.word_count",
		"issues":   "(SELECT COUNT(*) FROM issues i WHERE i.job_id = p.job_id AND i.url_id = p.url_id)",
		"inbound":  "p.inbound_edge_count",
		"outbound": "p.outbound_edge_count",
	}
	selectSQL := "SELECT " + selectCols + " FROM pages p" + joins +
		" WHERE p.job_id = ?" + filterClause.String() +
		orderClause(filter.SortBy, filter.SortDir, pageSorts, "p.id") + ", p.id ASC LIMIT ? OFFSET ?"

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
	sortBy := strings.ToLower(strings.TrimSpace(filter.SortBy))
	needsURLJoin := filter.URLPattern != "" || sortBy == "url"

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

	issueSorts := map[string]string{
		"url":      "u.normalized_url",
		"type":     "i.issue_type",
		"severity": "CASE i.severity WHEN 'error' THEN 1 WHEN 'warning' THEN 2 WHEN 'info' THEN 3 ELSE 4 END",
		"scope":    "i.scope",
	}
	selectSQL := "SELECT i.id, i.job_id, i.url_id, i.issue_type, i.severity, i.scope, i.details_json" +
		" FROM issues i" + joins +
		" WHERE i.job_id = ?" + filterClause.String() +
		orderClause(filter.SortBy, filter.SortDir, issueSorts, "i.id") + ", i.id ASC LIMIT ? OFFSET ?"

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

// QueryEdgesOffset returns link edges with offset pagination for HTTP tables.
func (db *DB) QueryEdgesOffset(
	jobID string, filter QueryFilter, limit, offset int,
) (*PagedResult[Edge], error) {
	sortBy := strings.ToLower(strings.TrimSpace(filter.SortBy))
	needsTargetJoin := filter.TargetDomain != "" || sortBy == "target"
	needsSourceJoin := filter.URLPattern != "" || sortBy == "source"

	var filterClause strings.Builder
	filterArgs := []any{}
	if filter.RelationType != "" {
		filterClause.WriteString(" AND e.relation_type = ?")
		filterArgs = append(filterArgs, filter.RelationType)
	}
	if filter.IsInternal != nil {
		filterClause.WriteString(" AND e.is_internal = ?")
		filterArgs = append(filterArgs, boolToInt(*filter.IsInternal))
	}
	if filter.URLPattern != "" {
		filterClause.WriteString(" AND (su.normalized_url LIKE ? OR e.declared_target_url LIKE ?)")
		like := "%" + filter.URLPattern + "%"
		filterArgs = append(filterArgs, like, like)
	}
	if filter.TargetDomain != "" {
		filterClause.WriteString(" AND tu.host = ?")
		filterArgs = append(filterArgs, filter.TargetDomain)
	}

	joins := ""
	if needsSourceJoin {
		joins += " JOIN urls su ON su.id = e.source_url_id"
	}
	if needsTargetJoin {
		joins += " LEFT JOIN urls tu ON tu.id = e.normalized_target_url_id"
	}

	countArgs := append([]any{jobID}, filterArgs...)
	countSQL := "SELECT COUNT(*) FROM edges e" + joins + " WHERE e.job_id = ?" + filterClause.String()
	var totalCount int
	if err := db.QueryRow(countSQL, countArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("counting edges for job %q: %w", jobID, err)
	}

	edgeSorts := map[string]string{
		"source":    "su.normalized_url",
		"target":    "COALESCE(tu.normalized_url, e.declared_target_url)",
		"anchor":    "e.anchor_text",
		"discovery": "e.discovery_mode",
		"status":    "e.target_status_code",
	}
	selectSQL := "SELECT e.id, e.job_id, e.source_url_id, e.normalized_target_url_id," +
		" e.source_kind, e.relation_type, e.rel_flags_json, e.discovery_mode," +
		" e.anchor_text, e.is_internal, e.declared_target_url," +
		" e.final_target_url_id, e.target_status_code FROM edges e" + joins +
		" WHERE e.job_id = ?" + filterClause.String() +
		orderClause(filter.SortBy, filter.SortDir, edgeSorts, "e.id") + ", e.id ASC LIMIT ? OFFSET ?"

	queryArgs := append(append([]any{jobID}, filterArgs...), limit, offset)
	rows, err := db.Query(selectSQL, queryArgs...)
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
	return &PagedResult[Edge]{Results: edges, TotalCount: totalCount}, nil
}

// QueryResponseCodesOffset returns fetch responses with offset pagination for HTTP tables.
func (db *DB) QueryResponseCodesOffset(
	jobID string, filter QueryFilter, limit, offset int,
) (*PagedResult[Fetch], error) {
	sortBy := strings.ToLower(strings.TrimSpace(filter.SortBy))
	needsURLJoin := filter.URLPattern != "" || sortBy == "url"

	var filterClause strings.Builder
	filterArgs := []any{}
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
	if filter.URLPattern != "" {
		filterClause.WriteString(" AND u.normalized_url LIKE ?")
		filterArgs = append(filterArgs, "%"+filter.URLPattern+"%")
	}

	joins := ""
	if needsURLJoin {
		joins = " JOIN urls u ON u.id = f.requested_url_id"
	}
	countArgs := append([]any{jobID}, filterArgs...)
	countSQL := "SELECT COUNT(*) FROM fetches f" + joins + " WHERE f.job_id = ?" + filterClause.String()
	var totalCount int
	if err := db.QueryRow(countSQL, countArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("counting fetches for job %q: %w", jobID, err)
	}

	responseSorts := map[string]string{
		"url":          "u.normalized_url",
		"status":       "f.status_code",
		"content_type": "f.content_type",
		"ttfb":         "f.ttfb_ms",
		"size":         "f.response_body_size",
	}
	selectSQL := "SELECT " + prefixedSelectColumns("f", fetchColumns) + " FROM fetches f" + joins +
		" WHERE f.job_id = ?" + filterClause.String() +
		orderClause(filter.SortBy, filter.SortDir, responseSorts, "f.id") + ", f.id ASC LIMIT ? OFFSET ?"
	queryArgs := append(append([]any{jobID}, filterArgs...), limit, offset)
	rows, err := db.Query(selectSQL, queryArgs...)
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
	return &PagedResult[Fetch]{Results: fetches, TotalCount: totalCount}, nil
}

// QueryAssetsOffset returns assets with offset pagination for HTTP tables.
func (db *DB) QueryAssetsOffset(
	jobID string, filter QueryFilter, limit, offset int,
) (*PagedResult[Asset], error) {
	var filterClause strings.Builder
	filterArgs := []any{}
	if filter.URLPattern != "" {
		filterClause.WriteString(" AND u.normalized_url LIKE ?")
		filterArgs = append(filterArgs, "%"+filter.URLPattern+"%")
	}
	if filter.ContentType != "" {
		filterClause.WriteString(" AND a.content_type LIKE ?")
		filterArgs = append(filterArgs, "%"+filter.ContentType+"%")
	}
	if filter.StatusCodeFamily != "" {
		lo, hi, err := statusCodeFamilyRange(filter.StatusCodeFamily)
		if err != nil {
			return nil, err
		}
		filterClause.WriteString(" AND a.status_code >= ? AND a.status_code <= ?")
		filterArgs = append(filterArgs, lo, hi)
	}

	joins := " JOIN urls u ON u.id = a.url_id"
	countArgs := append([]any{jobID}, filterArgs...)
	countSQL := "SELECT COUNT(*) FROM assets a" + joins + " WHERE a.job_id = ?" + filterClause.String()
	var totalCount int
	if err := db.QueryRow(countSQL, countArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("counting assets for job %q: %w", jobID, err)
	}
	assetSorts := map[string]string{
		"url":          "u.normalized_url",
		"content_type": "a.content_type",
		"transfer":     "COALESCE(a.transfer_size, a.content_length, 0)",
		"status":       "a.status_code",
		"cache":        "a.cache_control",
	}
	selectSQL := "SELECT " + prefixedSelectColumns("a", assetColumns) + " FROM assets a" + joins +
		" WHERE a.job_id = ?" + filterClause.String() +
		orderClause(filter.SortBy, filter.SortDir, assetSorts, "a.id") + ", a.id ASC LIMIT ? OFFSET ?"
	queryArgs := append(append([]any{jobID}, filterArgs...), limit, offset)
	rows, err := db.Query(selectSQL, queryArgs...)
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
	return &PagedResult[Asset]{Results: assets, TotalCount: totalCount}, nil
}

// QuerySitemapEntriesOffset returns sitemap entries with offset pagination for HTTP tables.
func (db *DB) QuerySitemapEntriesOffset(
	jobID string, filter QueryFilter, limit, offset int,
) (*PagedResult[SitemapEntry], error) {
	var filterClause strings.Builder
	filterArgs := []any{}
	sitemapKeyExpr := SitemapComparableURLSQL("s.url")
	sitemapMatchExpr := "EXISTS (SELECT 1 FROM pages p JOIN urls u ON u.id = p.url_id " +
		"WHERE p.job_id = s.job_id AND " + sitemapKeyExpr + " = " + SitemapComparableURLSQL("u.normalized_url") + ")"
	if filter.URLPattern != "" {
		filterClause.WriteString(" AND (s.url LIKE ? OR s.source_sitemap_url LIKE ?)")
		like := "%" + filter.URLPattern + "%"
		filterArgs = append(filterArgs, like, like)
	}
	if filter.StatusCodeFamily != "" {
		if filter.StatusCodeFamily == "matched" || filter.StatusCodeFamily == "in_crawl" {
			filterClause.WriteString(" AND " + sitemapMatchExpr)
		} else if filter.StatusCodeFamily == "not_in_crawl" || filter.StatusCodeFamily == "orphan" {
			filterClause.WriteString(" AND NOT " + sitemapMatchExpr)
		} else {
			filterClause.WriteString(" AND s.reconciliation_status = ?")
			filterArgs = append(filterArgs, filter.StatusCodeFamily)
		}
	}

	countArgs := append([]any{jobID}, filterArgs...)
	countSQL := "SELECT COUNT(DISTINCT " + sitemapKeyExpr + ") FROM sitemap_entries s WHERE s.job_id = ?" + filterClause.String()
	var totalCount int
	if err := db.QueryRow(countSQL, countArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("counting sitemap entries for job %q: %w", jobID, err)
	}
	sitemapSorts := map[string]string{
		"url":      "MAX(s.url)",
		"source":   "MAX(s.source_sitemap_url)",
		"lastmod":  "MAX(s.lastmod)",
		"priority": "MAX(s.priority)",
		"status":   sitemapMatchExpr,
	}
	selectSQL := `
		SELECT MIN(s.id), s.job_id, MAX(s.url), MAX(s.source_sitemap_url), MAX(s.source_host),
		       MAX(s.lastmod), MAX(s.changefreq), MAX(s.priority),
		       CASE
		         WHEN ` + sitemapMatchExpr + ` THEN 'in_crawl'
		         WHEN NOT ` + sitemapMatchExpr + ` THEN 'not_in_crawl'
		         ELSE MAX(s.reconciliation_status)
		       END AS reconciliation_status
		FROM sitemap_entries s WHERE s.job_id = ?` + filterClause.String() +
		" GROUP BY s.job_id, " + sitemapKeyExpr +
		orderClause(filter.SortBy, filter.SortDir, sitemapSorts, "MIN(s.id)") + ", MIN(s.id) ASC LIMIT ? OFFSET ?"
	queryArgs := append(append([]any{jobID}, filterArgs...), limit, offset)
	rows, err := db.Query(selectSQL, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying sitemap entries for job %q: %w", jobID, err)
	}
	defer rows.Close()

	entries := []SitemapEntry{}
	for rows.Next() {
		s, scanErr := scanSitemapEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning sitemap entry row: %w", scanErr)
		}
		entries = append(entries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sitemap entry rows: %w", err)
	}
	return &PagedResult[SitemapEntry]{Results: entries, TotalCount: totalCount}, nil
}
