// Package issues provides SEO issue detection for crawled pages.
package issues

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

// GlobalConfig holds thresholds for global issue detection.
type GlobalConfig struct {
	ThinContentThreshold int
	DeepPageThreshold    int
}

// DefaultGlobalConfig returns sensible defaults for global issue detection.
func DefaultGlobalConfig() GlobalConfig {
	return GlobalConfig{
		ThinContentThreshold: 200,
		DeepPageThreshold:    3,
	}
}

// DetectGlobalIssues runs all Phase 2 (global) issue detectors against a completed crawl job.
// Returns the total number of new issues inserted.
// The function is idempotent: it deletes any existing global issues before re-detecting.
func DetectGlobalIssues(db *storage.DB, jobID string, cfg GlobalConfig) (int, error) {
	previousIssues, err := loadExistingGlobalIssueInputs(db, jobID)
	if err != nil {
		return 0, err
	}

	// Idempotency guard: clear existing global issues so retries are safe.
	_, err = db.Exec("DELETE FROM issues WHERE job_id = ? AND scope = 'global'", jobID)
	if err != nil {
		return 0, fmt.Errorf("clearing existing global issues: %w", err)
	}

	detectors := []func(*storage.DB, string, GlobalConfig) (int, error){
		detectDuplicateTitles,
		detectDuplicateDescriptions,
		detectDuplicateContent,
		detectDuplicateH1,
		detectDuplicateH2,
		detectOrphanPages,
		detectDeepPages,
		detectHreflangNotReciprocal,
		detectBrokenHreflangTarget,
		detectCanonicalToNon200,
		detectCanonicalChain,
		detectCanonicalToRedirect,
		detectNonIndexableCanonical,
		detectUnlinkedCanonical,
		detectBrokenPaginationChain,
		detectPaginationCanonicalMismatch,
		detectSitemapNon200,
		detectCrawledNotInSitemap,
		detectInSitemapNotCrawled,
		detectInSitemapRobotsBlocked,
		detectHTTPToHTTPSMissing,
		detectJSOnlyNavigation,
		detectImageOver100KB,
		detectNoInternalOutlinks,
		detectNonIndexableInSitemap,
		detectURLInMultipleSitemaps,
		detectSitemapTooLarge,
	}

	var total int
	for _, detector := range detectors {
		count, err := detector(db, jobID, cfg)
		if err != nil {
			if restoreErr := restoreGlobalIssues(db, jobID, previousIssues); restoreErr != nil {
				return total, fmt.Errorf("%w; additionally failed to restore previous global issues: %v", err, restoreErr)
			}
			return total, err
		}
		total += count
	}

	return total, nil
}

func loadExistingGlobalIssueInputs(db *storage.DB, jobID string) ([]storage.IssueInput, error) {
	rows, err := db.Query("SELECT url_id, issue_type, severity, details_json FROM issues WHERE job_id = ? AND scope = 'global' ORDER BY id ASC", jobID)
	if err != nil {
		return nil, fmt.Errorf("loading existing global issues: %w", err)
	}
	defer rows.Close()

	var inputs []storage.IssueInput
	for rows.Next() {
		var urlID sql.NullInt64
		var details sql.NullString
		input := storage.IssueInput{JobID: jobID, Scope: "global"}
		if err := rows.Scan(&urlID, &input.IssueType, &input.Severity, &details); err != nil {
			return nil, fmt.Errorf("scanning existing global issue: %w", err)
		}
		if urlID.Valid {
			id := urlID.Int64
			input.URLID = &id
		}
		if details.Valid {
			detailsJSON := details.String
			input.DetailsJSON = &detailsJSON
		}
		inputs = append(inputs, input)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating existing global issues: %w", err)
	}
	return inputs, nil
}

func restoreGlobalIssues(db *storage.DB, jobID string, inputs []storage.IssueInput) error {
	if _, err := db.Exec("DELETE FROM issues WHERE job_id = ? AND scope = 'global'", jobID); err != nil {
		return fmt.Errorf("clearing partial global issues: %w", err)
	}
	if err := db.InsertIssuesBatch(inputs); err != nil {
		return fmt.Errorf("re-inserting previous global issues: %w", err)
	}
	return nil
}

func insertGlobalIssue(db *storage.DB, jobID string, urlID *int64, issueType, severity string, details map[string]any) error {
	detailsBytes, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshaling details for %q: %w", issueType, err)
	}
	detailsJSON := string(detailsBytes)
	_, err = db.InsertIssue(storage.IssueInput{
		JobID:       jobID,
		URLID:       urlID,
		IssueType:   issueType,
		Severity:    severity,
		Scope:       "global",
		DetailsJSON: &detailsJSON,
	})
	if err != nil {
		return fmt.Errorf("inserting global issue %q: %w", issueType, err)
	}
	return nil
}

type duplicateGroup struct {
	value     string
	urlIDs    []int64
	fetchSeqs []int64
}

// allowedDuplicateFields whitelists column names that may be interpolated into SQL
// for duplicate detection queries, preventing SQL injection.
var allowedDuplicateFields = map[string]bool{
	"title":            true,
	"meta_description": true,
	"content_hash":     true,
}

func queryDuplicateGroups(db *storage.DB, jobID, field string) ([]duplicateGroup, error) {
	if !allowedDuplicateFields[field] {
		return nil, fmt.Errorf("invalid duplicate detection field: %q", field)
	}

	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	query := fmt.Sprintf(`
		SELECT p.%s, GROUP_CONCAT(p.url_id), GROUP_CONCAT(f.fetch_seq)
		FROM pages p
		JOIN fetches f ON f.id = p.fetch_id
		WHERE p.job_id = ? AND p.%s IS NOT NULL AND p.%s != ''
		GROUP BY p.%s
		HAVING COUNT(*) > 1
	`, field, field, field, field)

	rows, err := db.Query(query, jobID)
	if err != nil {
		return nil, fmt.Errorf("querying duplicate groups for %q: %w", field, err)
	}
	defer rows.Close()

	groups := []duplicateGroup{}
	for rows.Next() {
		var group duplicateGroup
		var urlIDsRaw string
		var fetchSeqsRaw string
		if err := rows.Scan(&group.value, &urlIDsRaw, &fetchSeqsRaw); err != nil {
			return nil, fmt.Errorf("scanning duplicate group for %q: %w", field, err)
		}
		group.urlIDs = parseInt64List(urlIDsRaw)
		group.fetchSeqs = parseInt64List(fetchSeqsRaw)
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating duplicate groups for %q: %w", field, err)
	}

	return groups, nil
}

func detectDuplicateTitles(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	return detectDuplicateField(db, jobID, "title", "duplicate_title", "warning", "value")
}

func detectDuplicateDescriptions(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	return detectDuplicateField(db, jobID, "meta_description", "duplicate_description", "warning", "value")
}

func detectDuplicateContent(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	return detectDuplicateField(db, jobID, "content_hash", "duplicate_content", "warning", "contentHash")
}

func detectDuplicateField(db *storage.DB, jobID, field, issueType, severity, detailKey string) (int, error) {
	groups, err := queryDuplicateGroups(db, jobID, field)
	if err != nil {
		return 0, err
	}

	var total int
	for _, group := range groups {
		canonicalIndex := minIndex(group.fetchSeqs)
		for idx, urlID := range group.urlIDs {
			if idx == canonicalIndex {
				continue
			}
			details := map[string]any{
				detailKey:   group.value,
				"groupSize": len(group.urlIDs),
			}
			if err := insertGlobalIssue(db, jobID, &urlID, issueType, severity, details); err != nil {
				return total, err
			}
			total++
		}
	}

	return total, nil
}

func detectOrphanPages(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		WHERE p.job_id = ? AND p.inbound_edge_count = 0 AND u.discovered_via != 'seed'
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying orphan pages: %w", err)
	}
	defer rows.Close()

	urlIDs := []int64{}
	for rows.Next() {
		var urlID int64
		if err := rows.Scan(&urlID); err != nil {
			return 0, fmt.Errorf("scanning orphan page: %w", err)
		}
		urlIDs = append(urlIDs, urlID)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating orphan pages: %w", err)
	}

	for _, urlID := range urlIDs {
		if err := insertGlobalIssue(db, jobID, &urlID, "orphan_page", "warning", map[string]any{}); err != nil {
			return 0, err
		}
	}

	return len(urlIDs), nil
}

func detectDeepPages(db *storage.DB, jobID string, cfg GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, p.depth
		FROM pages p
		WHERE p.job_id = ? AND p.depth > ?
	`, jobID, cfg.DeepPageThreshold)
	if err != nil {
		return 0, fmt.Errorf("querying deep pages: %w", err)
	}
	defer rows.Close()

	type deepPage struct {
		urlID int64
		depth int
	}
	pages := []deepPage{}
	for rows.Next() {
		var page deepPage
		if err := rows.Scan(&page.urlID, &page.depth); err != nil {
			return 0, fmt.Errorf("scanning deep page: %w", err)
		}
		pages = append(pages, page)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating deep pages: %w", err)
	}

	var total int
	for _, page := range pages {
		var exists int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM issues WHERE job_id = ? AND url_id = ? AND issue_type = 'deep_page'`,
			jobID,
			page.urlID,
		).Scan(&exists); err != nil {
			return total, fmt.Errorf("checking existing deep_page issue: %w", err)
		}
		if exists > 0 {
			continue
		}
		if err := insertGlobalIssue(db, jobID, &page.urlID, "deep_page", "info", map[string]any{
			"depth":     page.depth,
			"threshold": cfg.DeepPageThreshold,
		}); err != nil {
			return total, err
		}
		total++
	}

	return total, nil
}

type hreflangEntry struct {
	Lang string `json:"lang"`
	URL  string `json:"url"`
}

func detectHreflangNotReciprocal(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, u.normalized_url, p.hreflang_json
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		WHERE p.job_id = ? AND p.hreflang_json IS NOT NULL AND p.hreflang_json != '' AND p.hreflang_json != '[]'
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying hreflang pages: %w", err)
	}
	defer rows.Close()

	type hreflangPage struct {
		urlID   int64
		url     string
		targets []string
	}
	pages := []hreflangPage{}
	targetMap := map[string]map[string]bool{}
	urlToID := map[string]int64{}

	for rows.Next() {
		var page hreflangPage
		var hreflangJSON string
		if err := rows.Scan(&page.urlID, &page.url, &hreflangJSON); err != nil {
			return 0, fmt.Errorf("scanning hreflang page: %w", err)
		}

		var entries []hreflangEntry
		if err := json.Unmarshal([]byte(hreflangJSON), &entries); err != nil {
			continue
		}

		page.targets = []string{}
		for _, entry := range entries {
			page.targets = append(page.targets, entry.URL)
		}
		pages = append(pages, page)
		urlToID[page.url] = page.urlID
		if targetMap[page.url] == nil {
			targetMap[page.url] = map[string]bool{}
		}
		for _, target := range page.targets {
			targetMap[page.url][target] = true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating hreflang pages: %w", err)
	}

	type issueRow struct {
		urlID     int64
		sourceURL string
		targetURL string
	}
	issues := []issueRow{}
	seen := map[string]bool{}
	for _, page := range pages {
		for _, targetURL := range page.targets {
			if targetURL == page.url {
				continue
			}
			key := page.url + "|" + targetURL
			if seen[key] {
				continue
			}
			seen[key] = true
			if targetMap[targetURL] != nil && targetMap[targetURL][page.url] {
				continue
			}
			issues = append(issues, issueRow{urlID: page.urlID, sourceURL: page.url, targetURL: targetURL})
		}
	}

	for _, issue := range issues {
		if err := insertGlobalIssue(db, jobID, &issue.urlID, "hreflang_not_reciprocal", "warning", map[string]any{
			"sourceUrl": issue.sourceURL,
			"targetUrl": issue.targetURL,
		}); err != nil {
			return 0, err
		}
	}

	return len(issues), nil
}

func detectBrokenHreflangTarget(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, p.hreflang_json
		FROM pages p
		WHERE p.job_id = ? AND p.hreflang_json IS NOT NULL AND p.hreflang_json != '' AND p.hreflang_json != '[]'
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying hreflang targets: %w", err)
	}
	defer rows.Close()

	type targetCheck struct {
		urlID     int64
		targetURL string
	}
	checks := []targetCheck{}
	for rows.Next() {
		var urlID int64
		var hreflangJSON string
		if err := rows.Scan(&urlID, &hreflangJSON); err != nil {
			return 0, fmt.Errorf("scanning hreflang target row: %w", err)
		}
		var entries []hreflangEntry
		if err := json.Unmarshal([]byte(hreflangJSON), &entries); err != nil {
			continue
		}
		for _, entry := range entries {
			checks = append(checks, targetCheck{urlID: urlID, targetURL: entry.URL})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating hreflang targets: %w", err)
	}

	var total int
	seen := map[string]bool{}
	for _, check := range checks {
		key := fmt.Sprintf("%d|%s", check.urlID, check.targetURL)
		if seen[key] {
			continue
		}
		seen[key] = true

		var statusCode int
		err := db.QueryRow(`
			SELECT f.status_code
			FROM urls u
			JOIN fetches f ON f.requested_url_id = u.id AND f.job_id = u.job_id
			WHERE u.job_id = ? AND u.normalized_url = ? AND f.status_code IS NOT NULL
			ORDER BY f.fetch_seq DESC LIMIT 1
		`, jobID, check.targetURL).Scan(&statusCode)
		if err != nil {
			continue
		}
		if statusCode == 200 {
			continue
		}

		if err := insertGlobalIssue(db, jobID, &check.urlID, "broken_hreflang_target", "error", map[string]any{
			"targetUrl":  check.targetURL,
			"statusCode": statusCode,
		}); err != nil {
			return total, err
		}
		total++
	}

	return total, nil
}

func detectCanonicalToNon200(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, p.canonical_url, p.canonical_status_code
		FROM pages p
		WHERE p.job_id = ?
			AND p.canonical_url IS NOT NULL AND p.canonical_url != ''
			AND p.canonical_status_code IS NOT NULL AND p.canonical_status_code != 200
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying canonical_to_non_200: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		urlID        int64
		canonicalURL string
		statusCode   int
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.urlID, &c.canonicalURL, &c.statusCode); err != nil {
			return 0, fmt.Errorf("scanning canonical_to_non_200: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating canonical_to_non_200: %w", err)
	}

	for _, c := range candidates {
		if err := insertGlobalIssue(db, jobID, &c.urlID, "canonical_to_non_200", "error", map[string]any{
			"canonicalUrl": c.canonicalURL,
			"statusCode":   c.statusCode,
		}); err != nil {
			return 0, err
		}
	}

	return len(candidates), nil
}

func detectCanonicalChain(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p1.url_id, p1.canonical_url, p2.canonical_url
		FROM pages p1
		JOIN urls u2 ON u2.job_id = p1.job_id AND u2.normalized_url = p1.canonical_url
		JOIN pages p2 ON p2.job_id = p1.job_id AND p2.url_id = u2.id
		JOIN urls u1 ON u1.job_id = p1.job_id AND u1.id = p1.url_id
		WHERE p1.job_id = ?
			AND p1.canonical_url IS NOT NULL AND p1.canonical_url != ''
			AND p2.canonical_url IS NOT NULL AND p2.canonical_url != ''
			AND p1.canonical_url != u1.normalized_url
			AND p2.canonical_url != p1.canonical_url
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying canonical_chain: %w", err)
	}
	defer rows.Close()

	type chain struct {
		urlID              int64
		canonicalURL       string
		targetCanonicalURL string
	}
	chains := []chain{}
	for rows.Next() {
		var c chain
		if err := rows.Scan(&c.urlID, &c.canonicalURL, &c.targetCanonicalURL); err != nil {
			return 0, fmt.Errorf("scanning canonical_chain: %w", err)
		}
		chains = append(chains, c)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating canonical_chain: %w", err)
	}

	for _, c := range chains {
		if err := insertGlobalIssue(db, jobID, &c.urlID, "canonical_chain", "warning", map[string]any{
			"canonicalUrl":       c.canonicalURL,
			"targetCanonicalUrl": c.targetCanonicalURL,
		}); err != nil {
			return 0, err
		}
	}

	return len(chains), nil
}

func detectCanonicalToRedirect(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, p.canonical_url, p.canonical_status_code
		FROM pages p
		WHERE p.job_id = ?
			AND p.canonical_url IS NOT NULL AND p.canonical_url != ''
			AND p.canonical_status_code IN (301, 302, 307, 308)
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying canonical_to_redirect: %w", err)
	}
	defer rows.Close()

	type redirect struct {
		urlID        int64
		canonicalURL string
		statusCode   int
	}
	redirects := []redirect{}
	for rows.Next() {
		var redirect redirect
		if err := rows.Scan(&redirect.urlID, &redirect.canonicalURL, &redirect.statusCode); err != nil {
			return 0, fmt.Errorf("scanning canonical_to_redirect: %w", err)
		}
		redirects = append(redirects, redirect)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating canonical_to_redirect: %w", err)
	}

	for _, redirect := range redirects {
		if err := insertGlobalIssue(db, jobID, &redirect.urlID, "canonical_to_redirect", "warning", map[string]any{
			"canonicalUrl": redirect.canonicalURL,
			"statusCode":   redirect.statusCode,
		}); err != nil {
			return 0, err
		}
	}

	return len(redirects), nil
}

// allowedPaginationColumns whitelists column names that may be interpolated into SQL
// for pagination chain queries, preventing SQL injection.
var allowedPaginationColumns = map[string]bool{
	"rel_next_url": true,
	"rel_prev_url": true,
}

func detectBrokenPaginationChain(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	type paginationEdge struct {
		urlID     int64
		targetURL string
		relType   string
	}
	edges := []paginationEdge{}

	for _, relation := range []struct {
		column  string
		relType string
	}{
		{column: "rel_next_url", relType: "next"},
		{column: "rel_prev_url", relType: "prev"},
	} {
		if !allowedPaginationColumns[relation.column] {
			return 0, fmt.Errorf("invalid pagination column: %q", relation.column)
		}
		// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
		query := fmt.Sprintf(`
			SELECT p.url_id, p.%s
			FROM pages p
			WHERE p.job_id = ? AND p.%s IS NOT NULL AND p.%s != ''
		`, relation.column, relation.column, relation.column)
		rows, err := db.Query(query, jobID)
		if err != nil {
			return 0, fmt.Errorf("querying %s pagination edges: %w", relation.relType, err)
		}

		for rows.Next() {
			var edge paginationEdge
			edge.relType = relation.relType
			if err := rows.Scan(&edge.urlID, &edge.targetURL); err != nil {
				rows.Close()
				return 0, fmt.Errorf("scanning %s pagination edge: %w", relation.relType, err)
			}
			edges = append(edges, edge)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, fmt.Errorf("iterating %s pagination edges: %w", relation.relType, err)
		}
		rows.Close()
	}

	var total int
	for _, edge := range edges {
		var statusCode int
		err := db.QueryRow(`
			SELECT f.status_code
			FROM urls u
			JOIN fetches f ON f.requested_url_id = u.id AND f.job_id = u.job_id
			WHERE u.job_id = ? AND u.normalized_url = ? AND f.status_code IS NOT NULL
			ORDER BY f.fetch_seq DESC LIMIT 1
		`, jobID, edge.targetURL).Scan(&statusCode)
		if err != nil {
			continue
		}
		if statusCode == 200 {
			continue
		}
		if err := insertGlobalIssue(db, jobID, &edge.urlID, "broken_pagination_chain", "warning", map[string]any{
			"targetUrl":  edge.targetURL,
			"statusCode": statusCode,
			"relType":    edge.relType,
		}); err != nil {
			return total, err
		}
		total++
	}

	return total, nil
}

func detectPaginationCanonicalMismatch(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, p.canonical_url, u.normalized_url
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		WHERE p.job_id = ?
			AND (p.rel_next_url IS NOT NULL OR p.rel_prev_url IS NOT NULL)
			AND p.canonical_url IS NOT NULL AND p.canonical_url != ''
			AND p.canonical_url != u.normalized_url
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying pagination_canonical_mismatch: %w", err)
	}
	defer rows.Close()

	type mismatch struct {
		urlID        int64
		canonicalURL string
		pageURL      string
	}
	mismatches := []mismatch{}
	for rows.Next() {
		var mismatch mismatch
		if err := rows.Scan(&mismatch.urlID, &mismatch.canonicalURL, &mismatch.pageURL); err != nil {
			return 0, fmt.Errorf("scanning pagination_canonical_mismatch: %w", err)
		}
		mismatches = append(mismatches, mismatch)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating pagination_canonical_mismatch: %w", err)
	}

	for _, mismatch := range mismatches {
		if err := insertGlobalIssue(db, jobID, &mismatch.urlID, "pagination_canonical_mismatch", "warning", map[string]any{
			"pageUrl":      mismatch.pageURL,
			"canonicalUrl": mismatch.canonicalURL,
		}); err != nil {
			return 0, err
		}
	}

	return len(mismatches), nil
}

func detectSitemapNon200(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT se.url, f.status_code, u.id
		FROM sitemap_entries se
		JOIN urls u ON u.job_id = se.job_id AND u.normalized_url = se.url
		JOIN (
			SELECT job_id, requested_url_id, MAX(fetch_seq) AS latest_fetch_seq
			FROM fetches
			WHERE job_id = ?
			GROUP BY job_id, requested_url_id
		) latest ON latest.job_id = se.job_id AND latest.requested_url_id = u.id
		JOIN fetches f ON f.job_id = latest.job_id
			AND f.requested_url_id = latest.requested_url_id
			AND f.fetch_seq = latest.latest_fetch_seq
		WHERE se.job_id = ? AND f.status_code IS NOT NULL AND f.status_code != 200
	`, jobID, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying sitemap_non_200: %w", err)
	}
	defer rows.Close()

	type sitemapIssue struct {
		url        string
		statusCode int
		urlID      int64
	}
	issues := []sitemapIssue{}
	for rows.Next() {
		var issue sitemapIssue
		if err := rows.Scan(&issue.url, &issue.statusCode, &issue.urlID); err != nil {
			return 0, fmt.Errorf("scanning sitemap_non_200: %w", err)
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating sitemap_non_200: %w", err)
	}

	for _, issue := range issues {
		if err := insertGlobalIssue(db, jobID, &issue.urlID, "sitemap_non_200", "warning", map[string]any{
			"sitemapUrl": issue.url,
			"statusCode": issue.statusCode,
		}); err != nil {
			return 0, err
		}
	}

	return len(issues), nil
}

func detectCrawledNotInSitemap(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, u.normalized_url
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		LEFT JOIN sitemap_entries se ON se.job_id = p.job_id AND se.url = u.normalized_url
		WHERE p.job_id = ? AND p.indexability_state = 'indexable' AND se.id IS NULL
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying crawled_not_in_sitemap: %w", err)
	}
	defer rows.Close()

	type missing struct {
		urlID int64
		url   string
	}
	missingURLs := []missing{}
	for rows.Next() {
		var item missing
		if err := rows.Scan(&item.urlID, &item.url); err != nil {
			return 0, fmt.Errorf("scanning crawled_not_in_sitemap: %w", err)
		}
		missingURLs = append(missingURLs, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating crawled_not_in_sitemap: %w", err)
	}

	for _, item := range missingURLs {
		if err := insertGlobalIssue(db, jobID, &item.urlID, "crawled_not_in_sitemap", "info", map[string]any{
			"url": item.url,
		}); err != nil {
			return 0, err
		}
	}

	return len(missingURLs), nil
}

func detectInSitemapNotCrawled(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT se.url
		FROM sitemap_entries se
		LEFT JOIN urls u ON u.job_id = se.job_id AND u.normalized_url = se.url
		WHERE se.job_id = ? AND (u.id IS NULL OR u.status = 'pending')
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying in_sitemap_not_crawled: %w", err)
	}
	defer rows.Close()

	urls := []string{}
	for rows.Next() {
		var sitemapURL string
		if err := rows.Scan(&sitemapURL); err != nil {
			return 0, fmt.Errorf("scanning in_sitemap_not_crawled: %w", err)
		}
		urls = append(urls, sitemapURL)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating in_sitemap_not_crawled: %w", err)
	}

	for _, sitemapURL := range urls {
		if err := insertGlobalIssue(db, jobID, nil, "in_sitemap_not_crawled", "info", map[string]any{
			"url": sitemapURL,
		}); err != nil {
			return 0, err
		}
	}

	return len(urls), nil
}

func detectInSitemapRobotsBlocked(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT se.url, u.id
		FROM sitemap_entries se
		JOIN urls u ON u.job_id = se.job_id AND u.normalized_url = se.url
		WHERE se.job_id = ? AND u.status = 'robots_blocked'
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying in_sitemap_robots_blocked: %w", err)
	}
	defer rows.Close()

	type blocked struct {
		urlID int64
		url   string
	}
	blockedURLs := []blocked{}
	for rows.Next() {
		var item blocked
		if err := rows.Scan(&item.url, &item.urlID); err != nil {
			return 0, fmt.Errorf("scanning in_sitemap_robots_blocked: %w", err)
		}
		blockedURLs = append(blockedURLs, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating in_sitemap_robots_blocked: %w", err)
	}

	for _, item := range blockedURLs {
		if err := insertGlobalIssue(db, jobID, &item.urlID, "in_sitemap_robots_blocked", "warning", map[string]any{
			"url": item.url,
		}); err != nil {
			return 0, err
		}
	}

	return len(blockedURLs), nil
}

func detectHTTPToHTTPSMissing(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT DISTINCT u.host
		FROM urls u
		WHERE u.job_id = ? AND u.normalized_url LIKE 'http://%%'
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying HTTP hosts: %w", err)
	}
	defer rows.Close()

	hosts := []string{}
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return 0, fmt.Errorf("scanning HTTP host: %w", err)
		}
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating HTTP hosts: %w", err)
	}

	var total int
	for _, host := range hosts {
		var redirectCount int
		if err := db.QueryRow(`
			SELECT COUNT(*)
			FROM redirect_hops rh
			WHERE rh.job_id = ?
				AND (
					rh.from_url = 'http://' || ?
					OR rh.from_url LIKE 'http://' || ? || '/%%'
					OR rh.from_url LIKE 'http://' || ? || ':%%'
				)
				AND rh.to_url LIKE 'https://%%'
		`, jobID, host, host, host).Scan(&redirectCount); err != nil {
			return total, fmt.Errorf("checking HTTP to HTTPS redirect for host %q: %w", host, err)
		}
		if redirectCount > 0 {
			continue
		}
		if err := insertGlobalIssue(db, jobID, nil, "http_to_https_missing", "info", map[string]any{
			"host": host,
		}); err != nil {
			return total, err
		}
		total++
	}

	return total, nil
}

func detectJSOnlyNavigation(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	// Find internal link edges that were ONLY discovered via browser rendering
	// i.e., edges with discovery_mode = 'browser' where there's no matching
	// static edge from the same source to the same target.
	rows, err := db.Query(`
		SELECT DISTINCT e.source_url_id, e.declared_target_url, u_src.normalized_url
		FROM edges e
		JOIN urls u_src ON u_src.id = e.source_url_id AND u_src.job_id = e.job_id
		WHERE e.job_id = ?
		  AND e.is_internal = 1
		  AND e.relation_type = 'link'
		  AND e.discovery_mode = 'browser'
		  AND NOT EXISTS (
		      SELECT 1 FROM edges e2
		      WHERE e2.job_id = e.job_id
		        AND e2.source_url_id = e.source_url_id
		        AND e2.declared_target_url = e.declared_target_url
		        AND e2.discovery_mode = 'static'
		  )
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying js_only_navigation: %w", err)
	}
	defer rows.Close()

	type jsOnlyLink struct {
		sourceURLID int64
		targetURL   string
		sourceURL   string
	}
	links := []jsOnlyLink{}
	for rows.Next() {
		var link jsOnlyLink
		if err := rows.Scan(&link.sourceURLID, &link.targetURL, &link.sourceURL); err != nil {
			return 0, fmt.Errorf("scanning js_only_navigation: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating js_only_navigation: %w", err)
	}

	for _, link := range links {
		if strings.Contains(link.targetURL, "#") {
			continue
		}
		normalizedTarget, normErr := urlutil.Normalize(link.targetURL)
		if normErr == nil && normalizedTarget == link.sourceURL {
			continue
		}
		if err := insertGlobalIssue(db, jobID, &link.sourceURLID, "js_only_navigation", "warning", map[string]any{
			"sourceUrl":     link.sourceURL,
			"targetUrl":     link.targetURL,
			"discoveryMode": "browser",
		}); err != nil {
			return 0, err
		}
	}

	return countIssuesByTypeAfterInsert(db, jobID, "js_only_navigation")
}

func countIssuesByTypeAfterInsert(db *storage.DB, jobID, issueType string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM issues WHERE job_id = ? AND issue_type = ?`, jobID, issueType).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting %s issues: %w", issueType, err)
	}
	return count, nil
}

func detectImageOver100KB(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT a.url_id, u.normalized_url, a.content_length
		FROM assets a
		JOIN urls u ON u.id = a.url_id AND u.job_id = a.job_id
		JOIN asset_references ar ON ar.job_id = a.job_id AND ar.asset_url_id = a.url_id AND ar.reference_type = 'img_src'
		WHERE a.job_id = ? AND a.content_length > 102400
		GROUP BY a.url_id
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying image_over_100kb: %w", err)
	}
	defer rows.Close()

	type bigImage struct {
		urlID         int64
		url           string
		contentLength int64
	}
	images := []bigImage{}
	for rows.Next() {
		var img bigImage
		if err := rows.Scan(&img.urlID, &img.url, &img.contentLength); err != nil {
			return 0, fmt.Errorf("scanning image_over_100kb: %w", err)
		}
		images = append(images, img)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating image_over_100kb: %w", err)
	}

	for _, img := range images {
		if err := insertGlobalIssue(db, jobID, &img.urlID, "image_over_100kb", "warning", map[string]any{
			"url":    img.url,
			"sizeKB": img.contentLength / 1024,
		}); err != nil {
			return 0, err
		}
	}

	return len(images), nil
}

func detectNoInternalOutlinks(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id
		FROM pages p
		WHERE p.job_id = ?
			AND NOT EXISTS (
				SELECT 1 FROM edges e
				WHERE e.job_id = p.job_id
					AND e.source_url_id = p.url_id
					AND e.is_internal = 1
					AND e.relation_type = 'link'
			)
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying no_internal_outlinks: %w", err)
	}
	defer rows.Close()

	urlIDs := []int64{}
	for rows.Next() {
		var urlID int64
		if err := rows.Scan(&urlID); err != nil {
			return 0, fmt.Errorf("scanning no_internal_outlinks: %w", err)
		}
		urlIDs = append(urlIDs, urlID)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating no_internal_outlinks: %w", err)
	}

	for _, urlID := range urlIDs {
		if err := insertGlobalIssue(db, jobID, &urlID, "no_internal_outlinks", "warning", map[string]any{}); err != nil {
			return 0, err
		}
	}

	return len(urlIDs), nil
}

func minIndex(values []int64) int {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	index := 0
	for i := 1; i < len(values); i++ {
		if values[i] < min {
			min = values[i]
			index = i
		}
	}
	return index
}

func parseInt64List(raw string) []int64 {
	parts := strings.Split(raw, ",")
	values := make([]int64, 0, len(parts))
	for _, part := range parts {
		value, _ := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		values = append(values, value)
	}
	return values
}

// detectDuplicateH1 finds pages with the same H1 text.
func detectDuplicateH1(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT json_extract(p.h1_json, '$[0]') AS first_h1, GROUP_CONCAT(p.url_id), GROUP_CONCAT(f.fetch_seq)
		FROM pages p
		JOIN fetches f ON f.id = p.fetch_id
		WHERE p.job_id = ? AND p.h1_json IS NOT NULL AND p.h1_json != '[]' AND p.h1_json != ''
		GROUP BY first_h1
		HAVING COUNT(*) > 1
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying duplicate H1s: %w", err)
	}

	// Collect all groups first (close rows before writing)
	groups := []duplicateGroup{}
	for rows.Next() {
		var g duplicateGroup
		var urlIDsRaw, fetchSeqsRaw string
		if err := rows.Scan(&g.value, &urlIDsRaw, &fetchSeqsRaw); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scanning duplicate H1 group: %w", err)
		}
		g.urlIDs = parseInt64List(urlIDsRaw)
		g.fetchSeqs = parseInt64List(fetchSeqsRaw)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var total int
	for _, group := range groups {
		canonicalIndex := minIndex(group.fetchSeqs)
		for idx, urlID := range group.urlIDs {
			if idx == canonicalIndex {
				continue
			}
			if err := insertGlobalIssue(db, jobID, &urlID, "duplicate_h1", "warning", map[string]any{
				"value":     group.value,
				"groupSize": len(group.urlIDs),
			}); err != nil {
				return total, err
			}
			total++
		}
	}
	return total, nil
}

// detectDuplicateH2 finds pages with the same H2 text.
func detectDuplicateH2(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT json_extract(p.h2_json, '$[0]') AS first_h2, GROUP_CONCAT(p.url_id), GROUP_CONCAT(f.fetch_seq)
		FROM pages p
		JOIN fetches f ON f.id = p.fetch_id
		WHERE p.job_id = ? AND p.h2_json IS NOT NULL AND p.h2_json != '[]' AND p.h2_json != ''
		GROUP BY first_h2
		HAVING COUNT(*) > 1
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying duplicate H2s: %w", err)
	}

	groups := []duplicateGroup{}
	for rows.Next() {
		var g duplicateGroup
		var urlIDsRaw, fetchSeqsRaw string
		if err := rows.Scan(&g.value, &urlIDsRaw, &fetchSeqsRaw); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scanning duplicate H2 group: %w", err)
		}
		g.urlIDs = parseInt64List(urlIDsRaw)
		g.fetchSeqs = parseInt64List(fetchSeqsRaw)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var total int
	for _, group := range groups {
		canonicalIndex := minIndex(group.fetchSeqs)
		for idx, urlID := range group.urlIDs {
			if idx == canonicalIndex {
				continue
			}
			if err := insertGlobalIssue(db, jobID, &urlID, "duplicate_h2", "warning", map[string]any{
				"value":     group.value,
				"groupSize": len(group.urlIDs),
			}); err != nil {
				return total, err
			}
			total++
		}
	}
	return total, nil
}

// detectNonIndexableCanonical finds pages whose canonical points to a noindexed page.
func detectNonIndexableCanonical(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p1.url_id, p1.canonical_url, p2.indexability_state
		FROM pages p1
		JOIN urls u2 ON u2.job_id = p1.job_id AND u2.normalized_url = p1.canonical_url
		JOIN pages p2 ON p2.job_id = p1.job_id AND p2.url_id = u2.id
		JOIN urls u1 ON u1.job_id = p1.job_id AND u1.id = p1.url_id
		WHERE p1.job_id = ?
			AND p1.canonical_url IS NOT NULL AND p1.canonical_url != ''
			AND p1.canonical_url != u1.normalized_url
			AND p2.indexability_state != 'indexable'
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying non_indexable_canonical: %w", err)
	}

	type candidate struct {
		urlID              int64
		canonicalURL       string
		targetIndexability string
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.urlID, &c.canonicalURL, &c.targetIndexability); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scanning non_indexable_canonical: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var total int
	for _, c := range candidates {
		if err := insertGlobalIssue(db, jobID, &c.urlID, "non_indexable_canonical", "warning", map[string]any{
			"canonicalUrl":       c.canonicalURL,
			"targetIndexability": c.targetIndexability,
		}); err != nil {
			return total, err
		}
		total++
	}
	return total, nil
}

// detectNonIndexableInSitemap finds URLs in sitemap that are not indexable.
func detectNonIndexableInSitemap(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT se.url, p.indexability_state, u.id
		FROM sitemap_entries se
		JOIN urls u ON u.job_id = se.job_id AND u.normalized_url = se.url
		JOIN pages p ON p.job_id = se.job_id AND p.url_id = u.id
		WHERE se.job_id = ? AND p.indexability_state != 'indexable'
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying non_indexable_in_sitemap: %w", err)
	}
	defer rows.Close()

	type entry struct {
		url               string
		indexabilityState string
		urlID             int64
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.url, &e.indexabilityState, &e.urlID); err != nil {
			return 0, fmt.Errorf("scanning non_indexable_in_sitemap: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating non_indexable_in_sitemap: %w", err)
	}

	for _, e := range entries {
		if err := insertGlobalIssue(db, jobID, &e.urlID, "non_indexable_in_sitemap", "warning", map[string]any{
			"url":               e.url,
			"indexabilityState": e.indexabilityState,
		}); err != nil {
			return 0, err
		}
	}
	return len(entries), nil
}

// detectURLInMultipleSitemaps finds URLs that appear in more than one sitemap file.
func detectURLInMultipleSitemaps(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT se.url, COUNT(DISTINCT se.source_sitemap_url) as sitemap_count
		FROM sitemap_entries se
		WHERE se.job_id = ?
		GROUP BY se.url
		HAVING sitemap_count > 1
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying url_in_multiple_sitemaps: %w", err)
	}
	defer rows.Close()

	type multiEntry struct {
		url          string
		sitemapCount int
	}
	entries := []multiEntry{}
	for rows.Next() {
		var e multiEntry
		if err := rows.Scan(&e.url, &e.sitemapCount); err != nil {
			return 0, fmt.Errorf("scanning url_in_multiple_sitemaps: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating url_in_multiple_sitemaps: %w", err)
	}

	for _, e := range entries {
		if err := insertGlobalIssue(db, jobID, nil, "url_in_multiple_sitemaps", "info", map[string]any{
			"url":          e.url,
			"sitemapCount": e.sitemapCount,
		}); err != nil {
			return 0, err
		}
	}
	return len(entries), nil
}

// detectSitemapTooLarge finds sitemaps with more than 50K entries.
func detectSitemapTooLarge(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT se.source_sitemap_url, COUNT(*) as entry_count
		FROM sitemap_entries se
		WHERE se.job_id = ?
		GROUP BY se.source_sitemap_url
		HAVING entry_count > 50000
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying sitemap_too_large: %w", err)
	}
	defer rows.Close()

	type largeSitemap struct {
		sitemapURL string
		entryCount int
	}
	sitemaps := []largeSitemap{}
	for rows.Next() {
		var s largeSitemap
		if err := rows.Scan(&s.sitemapURL, &s.entryCount); err != nil {
			return 0, fmt.Errorf("scanning sitemap_too_large: %w", err)
		}
		sitemaps = append(sitemaps, s)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating sitemap_too_large: %w", err)
	}

	for _, s := range sitemaps {
		if err := insertGlobalIssue(db, jobID, nil, "sitemap_too_large", "warning", map[string]any{
			"sitemapUrl": s.sitemapURL,
			"entryCount": s.entryCount,
		}); err != nil {
			return 0, err
		}
	}
	return len(sitemaps), nil
}

// detectUnlinkedCanonical finds canonical URLs that have zero inbound internal link edges.
func detectUnlinkedCanonical(db *storage.DB, jobID string, _ GlobalConfig) (int, error) {
	rows, err := db.Query(`
		SELECT p.url_id, p.canonical_url
		FROM pages p
		JOIN urls u ON u.job_id = p.job_id AND u.id = p.url_id
		WHERE p.job_id = ?
			AND p.canonical_url IS NOT NULL AND p.canonical_url != ''
			AND p.canonical_url != u.normalized_url
			AND NOT EXISTS (
				SELECT 1 FROM edges e
				WHERE e.job_id = p.job_id
					AND e.declared_target_url = p.canonical_url
					AND e.is_internal = 1
					AND e.relation_type = 'link'
			)
	`, jobID)
	if err != nil {
		return 0, fmt.Errorf("querying unlinked_canonical: %w", err)
	}

	type unlinked struct {
		urlID        int64
		canonicalURL string
	}
	items := []unlinked{}
	for rows.Next() {
		var item unlinked
		if err := rows.Scan(&item.urlID, &item.canonicalURL); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scanning unlinked_canonical: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var total int
	for _, item := range items {
		if err := insertGlobalIssue(db, jobID, &item.urlID, "unlinked_canonical", "warning", map[string]any{
			"canonicalUrl": item.canonicalURL,
		}); err != nil {
			return total, err
		}
		total++
	}
	return total, nil
}
