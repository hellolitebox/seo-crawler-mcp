package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

// reportExtrasLimit caps the number of rows returned per array in /report.
// Sites with more than this need to query the dedicated endpoints.
const reportExtrasLimit = 50000
const reportURLPreviewLimit = 200
const reportSitemapPreviewLimit = 100
const reportEdgePreviewLimit = 100
const reportAssetReferencePreviewLimit = 500
const reportResponseCodePreviewLimit = 50
const reportSecurityPreviewLimit = 100
const reportURLClusterPreviewLimit = 50

// loadReportExtras populates the auxiliary arrays the report UI consumes
// (sitemap_entries, robots_directives, crawl_events, urls, edges, etc.).
// Errors are logged and the corresponding field stays empty so a partial
// failure can't fail the whole report.
func loadReportExtras(ctx context.Context, db *storage.DB, jobID string) map[string]any {
	out := map[string]any{
		"external_links":       []any{},
		"response_codes":       []any{},
		"robots_directives":    []any{},
		"sitemap_entries":      []any{},
		"urls":                 []any{},
		"internal_edges":       []any{},
		"assets":               []any{},
		"asset_references":     []any{},
		"redirect_hops":        []any{},
		"llms_findings":        []any{},
		"crawl_events":         []any{},
		"metrics":              []any{},
		"psi_audits":           []any{},
		"axe_audits":           []any{},
		"security":             []any{},
		"agent_readiness":      []any{},
		"markdown_negotiation": []any{},
		"url_clusters":         []any{},
		"url_variant_issues":   []any{},
	}

	if entries, summary, err := loadSitemapEntries(ctx, db, jobID); err != nil {
		log.Printf("report_extras: sitemap_entries: %v", err)
	} else {
		out["sitemap_entries"] = entries
		out["sitemap_summary"] = summary
	}
	if v, err := loadRobotsDirectives(ctx, db, jobID); err != nil {
		log.Printf("report_extras: robots_directives: %v", err)
	} else {
		out["robots_directives"] = v
	}
	if urls, total, err := loadURLs(ctx, db, jobID); err != nil {
		log.Printf("report_extras: urls: %v", err)
	} else {
		out["urls"] = urls
		out["urls_total_count"] = total
	}
	if v, err := loadAssets(ctx, db, jobID); err != nil {
		log.Printf("report_extras: assets: %v", err)
	} else {
		out["assets"] = v
	}
	if v, err := loadAssetReferences(ctx, db, jobID); err != nil {
		log.Printf("report_extras: asset_references: %v", err)
	} else {
		out["asset_references"] = v
	}
	if internalEdges, externalLinks, internalTotal, externalTotal, err := loadEdges(ctx, db, jobID); err != nil {
		log.Printf("report_extras: edges: %v", err)
	} else {
		out["internal_edges"] = internalEdges
		out["external_links"] = externalLinks
		out["internal_edges_total_count"] = internalTotal
		out["external_links_total_count"] = externalTotal
	}
	if v, err := loadRedirectHops(ctx, db, jobID); err != nil {
		log.Printf("report_extras: redirect_hops: %v", err)
	} else {
		out["redirect_hops"] = v
	}
	if v, err := loadLlmsFindings(ctx, db, jobID); err != nil {
		log.Printf("report_extras: llms_findings: %v", err)
	} else {
		out["llms_findings"] = v
	}
	if v, err := loadResponseCodes(ctx, db, jobID); err != nil {
		log.Printf("report_extras: response_codes: %v", err)
	} else {
		out["response_codes"] = v
	}
	if events, metrics, psi, axe, markdownNegotiation, err := loadEventsAndAudits(ctx, db, jobID); err != nil {
		log.Printf("report_extras: crawl_events: %v", err)
	} else {
		out["crawl_events"] = events
		out["metrics"] = metrics
		out["psi_audits"] = psi
		out["axe_audits"] = axe
		out["markdown_negotiation"] = markdownNegotiation
	}
	if security, summary, err := loadSecurityHeaders(ctx, db, jobID); err != nil {
		log.Printf("report_extras: security: %v", err)
	} else {
		out["security"] = security
		out["security_summary"] = summary
	}
	if v, err := loadAgentReadiness(ctx, db, jobID); err != nil {
		log.Printf("report_extras: agent_readiness: %v", err)
	} else {
		out["agent_readiness"] = v
	}
	if clusters, issues, summary, err := loadURLClusters(ctx, db, jobID); err != nil {
		log.Printf("report_extras: url_clusters: %v", err)
	} else {
		out["url_clusters"] = clusters
		out["url_variant_issues"] = issues
		out["url_cluster_summary"] = summary
	}

	return out
}

// nullString returns *string from sql.NullString (nil when invalid).
func nullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

// nullInt64 returns *int64 from sql.NullInt64.
func nullInt64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nullFloat64 returns *float64 from sql.NullFloat64.
func nullFloat64(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func loadSitemapEntries(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, map[string]any, error) {
	summary, err := loadSitemapSummary(ctx, db, jobID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, url, source_sitemap_url, source_host,
		       lastmod, changefreq, priority, reconciliation_status
		FROM sitemap_entries WHERE job_id = ? ORDER BY id ASC LIMIT ?`, jobID, reportSitemapPreviewLimit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id                   int64
			jid, u, ssu, sh, rec string
			lastmod, changefreq  sql.NullString
			priority             sql.NullFloat64
		)
		if err := rows.Scan(&id, &jid, &u, &ssu, &sh, &lastmod, &changefreq, &priority, &rec); err != nil {
			return nil, nil, err
		}
		out = append(out, map[string]any{
			"id":                    id,
			"job_id":                jid,
			"url":                   u,
			"source_sitemap_url":    ssu,
			"source_host":           sh,
			"lastmod":               nullString(lastmod),
			"changefreq":            nullString(changefreq),
			"priority":              nullFloat64(priority),
			"reconciliation_status": rec,
		})
	}
	return out, summary, rows.Err()
}

func loadSitemapSummary(ctx context.Context, db *storage.DB, jobID string) (map[string]any, error) {
	var total, inCrawl, notInCrawl int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT url),
		       COUNT(DISTINCT CASE WHEN reconciliation_status IN ('in_crawl', 'matched') THEN url END),
		       COUNT(DISTINCT CASE WHEN reconciliation_status IN ('not_in_crawl', 'orphan') THEN url END)
		FROM sitemap_entries WHERE job_id = ?`, jobID).Scan(&total, &inCrawl, &notInCrawl); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT source_sitemap_url,
		       COUNT(*) AS entries,
		       COALESCE(SUM(CASE WHEN reconciliation_status IN ('in_crawl', 'matched') THEN 1 ELSE 0 END), 0) AS in_crawl,
		       COALESCE(SUM(CASE WHEN reconciliation_status IN ('not_in_crawl', 'orphan') THEN 1 ELSE 0 END), 0) AS not_in_crawl,
		       MAX(lastmod) AS latest_lastmod
		FROM sitemap_entries
		WHERE job_id = ?
		GROUP BY source_sitemap_url
		ORDER BY source_sitemap_url ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := []map[string]any{}
	for rows.Next() {
		var source string
		var entries, sourceInCrawl, sourceNotInCrawl int
		var latest sql.NullString
		if err := rows.Scan(&source, &entries, &sourceInCrawl, &sourceNotInCrawl, &latest); err != nil {
			return nil, err
		}
		sources = append(sources, map[string]any{
			"url":           source,
			"entries":       entries,
			"inCrawl":       sourceInCrawl,
			"notInCrawl":    sourceNotInCrawl,
			"latestLastmod": nullString(latest),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"totalEntries": total,
		"inCrawl":      inCrawl,
		"notInCrawl":   notInCrawl,
		"sources":      sources,
	}, nil
}

func loadRobotsDirectives(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, host, user_agent, rule_type, path_pattern, source_url
		FROM robots_directives WHERE job_id = ? LIMIT ?`, jobID, reportExtrasLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id                            int64
			jid, host, ua, rt, pp, srcURL string
		)
		if err := rows.Scan(&id, &jid, &host, &ua, &rt, &pp, &srcURL); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":           id,
			"job_id":       jid,
			"host":         host,
			"user_agent":   ua,
			"rule_type":    rt,
			"path_pattern": pp,
			"source_url":   srcURL,
		})
	}
	return out, rows.Err()
}

func loadAgentReadiness(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, category, check_key, status, score, target_url,
		       endpoint, method, request_headers_json, response_status,
		       response_headers_json, evidence_json, recommendation,
		       resources_json, checked_at
		FROM agent_readiness_checks
		WHERE job_id = ?
		ORDER BY category ASC, check_key ASC, target_url ASC
		LIMIT ?`, jobID, reportExtrasLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                              int64
			score                                           int64
			jid, category, checkKey, status, targetURL      string
			endpoint, method, evidenceJSON, resourcesJSON   string
			checkedAt                                       string
			requestHeaders, responseHeaders, recommendation sql.NullString
			responseStatus                                  sql.NullInt64
		)
		if err := rows.Scan(
			&id, &jid, &category, &checkKey, &status, &score, &targetURL,
			&endpoint, &method, &requestHeaders, &responseStatus,
			&responseHeaders, &evidenceJSON, &recommendation, &resourcesJSON, &checkedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":                    id,
			"job_id":                jid,
			"category":              category,
			"check_key":             checkKey,
			"status":                status,
			"score":                 score,
			"target_url":            targetURL,
			"endpoint":              endpoint,
			"method":                method,
			"request_headers_json":  nullString(requestHeaders),
			"response_status":       nullInt64(responseStatus),
			"response_headers_json": nullString(responseHeaders),
			"evidence_json":         evidenceJSON,
			"recommendation":        nullString(recommendation),
			"resources_json":        resourcesJSON,
			"checked_at":            checkedAt,
		})
	}
	return out, rows.Err()
}

func loadURLs(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, int, error) {
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM urls WHERE job_id = ?`, jobID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, normalized_url, host, status, is_internal, discovered_via, created_at
		FROM urls WHERE job_id = ? ORDER BY id ASC LIMIT ?`, jobID, reportURLPreviewLimit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id                            int64
			jid, nu, host, status, dv, ca string
			isInternal                    int64
		)
		if err := rows.Scan(&id, &jid, &nu, &host, &status, &isInternal, &dv, &ca); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]any{
			"id":             id,
			"job_id":         jid,
			"normalized_url": nu,
			"host":           host,
			"status":         status,
			"is_internal":    isInternal,
			"discovered_via": dv,
			"created_at":     ca,
		})
	}
	return out, total, rows.Err()
}

type urlVariantCluster struct {
	PageKey       string
	PrimaryURL    string
	CanonicalURL  string
	ContentHash   string
	PageURLs      map[string]bool
	ContentHashes map[string]bool
	CanonicalKeys map[string]bool
	Variants      map[string]map[string]any
}

func loadURLClusters(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, []map[string]any, map[string]any, error) {
	query := "SELECT p.id, u.normalized_url, ru.normalized_url, fu.normalized_url, p.canonical_url, p.content_hash, p.indexability_state, f.status_code " +
		"FROM pages p " +
		"JOIN urls u ON u.id = p.url_id " +
		"JOIN fetches f ON f.id = p.fetch_id " +
		"JOIN urls ru ON ru.id = f.requested_url_id " +
		"LEFT JOIN urls fu ON fu.id = f.final_url_id " +
		"WHERE p.job_id = ? ORDER BY p.id ASC LIMIT ?"
	rows, err := db.QueryContext(ctx, query, jobID, reportExtrasLimit)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	clustersByKey := map[string]*urlVariantCluster{}
	for rows.Next() {
		var (
			pageID                    int64
			pageURL, requestedURL     string
			finalURL, canonicalURL    sql.NullString
			contentHash, indexability sql.NullString
			statusCode                sql.NullInt64
		)
		if err := rows.Scan(&pageID, &pageURL, &requestedURL, &finalURL, &canonicalURL, &contentHash, &indexability, &statusCode); err != nil {
			return nil, nil, nil, err
		}

		identityURL := pageURL
		if canonicalURL.Valid && strings.TrimSpace(canonicalURL.String) != "" {
			identityURL = canonicalURL.String
		}
		pageKey := pageIdentityKey(identityURL)
		if pageKey == "" {
			pageKey = pageIdentityKey(pageURL)
		}
		if pageKey == "" {
			continue
		}

		cluster := clustersByKey[pageKey]
		if cluster == nil {
			cluster = &urlVariantCluster{
				PageKey:       pageKey,
				PrimaryURL:    pageURL,
				PageURLs:      map[string]bool{},
				ContentHashes: map[string]bool{},
				CanonicalKeys: map[string]bool{},
				Variants:      map[string]map[string]any{},
			}
			clustersByKey[pageKey] = cluster
		}
		cluster.PageURLs[pageURL] = true
		if contentHash.Valid && strings.TrimSpace(contentHash.String) != "" {
			cluster.ContentHashes[contentHash.String] = true
			if cluster.ContentHash == "" {
				cluster.ContentHash = contentHash.String
			}
		}
		if canonicalURL.Valid && strings.TrimSpace(canonicalURL.String) != "" {
			cluster.CanonicalURL = canonicalURL.String
			if key := pageIdentityKey(canonicalURL.String); key != "" {
				cluster.CanonicalKeys[key] = true
			}
		}

		statusValue := int64(0)
		if statusCode.Valid {
			statusValue = statusCode.Int64
		}

		addURLVariant(cluster, requestedURL, "requested", pageID, statusValue, indexability)
		if finalURL.Valid && strings.TrimSpace(finalURL.String) != "" {
			addURLVariant(cluster, finalURL.String, "final", pageID, statusValue, indexability)
		} else {
			addURLVariant(cluster, pageURL, "final", pageID, statusValue, indexability)
		}
		if canonicalURL.Valid && strings.TrimSpace(canonicalURL.String) != "" {
			addURLVariant(cluster, canonicalURL.String, "canonical", pageID, statusValue, indexability)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	clusters := make([]map[string]any, 0, len(clustersByKey))
	issues := []map[string]any{}
	variantClusterCount := 0
	issueClusterCount := 0
	for _, cluster := range clustersByKey {
		clusterType, severity := classifyURLVariantCluster(cluster)
		hasIssue := severity != ""
		if len(cluster.Variants) > 1 {
			variantClusterCount++
		}
		if hasIssue {
			issueClusterCount++
		}
		variants := make([]map[string]any, 0, len(cluster.Variants))
		for _, v := range cluster.Variants {
			variants = append(variants, v)
		}
		clusters = append(clusters, map[string]any{
			"pageKey":       cluster.PageKey,
			"primaryUrl":    cluster.PrimaryURL,
			"canonicalUrl":  emptyStringNil(cluster.CanonicalURL),
			"contentHash":   emptyStringNil(cluster.ContentHash),
			"variantCount":  len(cluster.Variants),
			"pageCount":     len(cluster.PageURLs),
			"clusterType":   clusterType,
			"hasIssue":      hasIssue,
			"issueSeverity": emptyStringNil(severity),
			"variants":      variants,
		})
		if hasIssue {
			details, _ := json.Marshal(map[string]any{
				"pageKey":      cluster.PageKey,
				"variantCount": len(cluster.Variants),
				"pageCount":    len(cluster.PageURLs),
				"clusterType":  clusterType,
			})
			issues = append(issues, map[string]any{
				"url":         cluster.PrimaryURL,
				"issueType":   "duplicate_url_variants",
				"severity":    severity,
				"scope":       "page",
				"detailsJson": string(details),
			})
		}
	}
	sort.Slice(clusters, func(i, j int) bool {
		iIssue, _ := clusters[i]["hasIssue"].(bool)
		jIssue, _ := clusters[j]["hasIssue"].(bool)
		if iIssue != jIssue {
			return iIssue
		}
		iVariants, _ := clusters[i]["variantCount"].(int)
		jVariants, _ := clusters[j]["variantCount"].(int)
		if iVariants != jVariants {
			return iVariants > jVariants
		}
		return strings.Compare(fmtAnyString(clusters[i]["primaryUrl"]), fmtAnyString(clusters[j]["primaryUrl"])) < 0
	})
	if len(clusters) > reportURLClusterPreviewLimit {
		clusters = clusters[:reportURLClusterPreviewLimit]
	}
	return clusters, issues, map[string]any{
		"totalClusters":        len(clustersByKey),
		"variantClusters":      variantClusterCount,
		"variantIssueClusters": issueClusterCount,
	}, nil
}

func fmtAnyString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func pageIdentityKey(rawURL string) string {
	key, err := urlutil.PageIdentityKey(rawURL)
	if err == nil {
		return key
	}
	return strings.TrimRight(strings.TrimSpace(rawURL), "/")
}

func addURLVariant(cluster *urlVariantCluster, rawURL, kind string, pageID, statusCode int64, indexability sql.NullString) {
	if strings.TrimSpace(rawURL) == "" {
		return
	}
	key := kind + "\x00" + rawURL
	if _, exists := cluster.Variants[key]; exists {
		return
	}
	cluster.Variants[key] = map[string]any{
		"url":               rawURL,
		"kind":              kind,
		"pageId":            pageID,
		"statusCode":        statusCode,
		"indexabilityState": nullString(indexability),
		"pageKey":           pageIdentityKey(rawURL),
	}
}

func classifyURLVariantCluster(cluster *urlVariantCluster) (string, string) {
	if len(cluster.PageURLs) < 2 {
		return "redirect_consolidated", ""
	}
	if len(cluster.ContentHashes) > 1 {
		return "distinct_content", ""
	}
	if len(cluster.CanonicalKeys) == 1 {
		for key := range cluster.CanonicalKeys {
			if key == cluster.PageKey {
				return "canonicalized_duplicate", "info"
			}
		}
	}
	return "duplicate_content_variant", "warning"
}

func emptyStringNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func loadAssets(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.job_id, a.url_id, a.content_type, a.content_encoding,
		       a.cache_control, a.transfer_size, a.decoded_size,
		       a.status_code, a.content_length,
		       u.normalized_url
		FROM assets a JOIN urls u ON u.id = a.url_id
		WHERE a.job_id = ? LIMIT ?`, jobID, reportExtrasLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id, urlID                                  int64
			jid, urlStr                                string
			contentType, contentEncoding, cacheControl sql.NullString
			transferSize, decodedSize                  sql.NullInt64
			statusCode, contentLength                  sql.NullInt64
		)
		if err := rows.Scan(&id, &jid, &urlID, &contentType, &contentEncoding, &cacheControl, &transferSize, &decodedSize, &statusCode, &contentLength, &urlStr); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":               id,
			"job_id":           jid,
			"url_id":           urlID,
			"content_type":     nullString(contentType),
			"content_encoding": nullString(contentEncoding),
			"cache_control":    nullString(cacheControl),
			"transfer_size":    nullInt64(transferSize),
			"decoded_size":     nullInt64(decodedSize),
			"status_code":      nullInt64(statusCode),
			"content_length":   nullInt64(contentLength),
			"url":              urlStr,
		})
	}
	return out, rows.Err()
}

func loadAssetReferences(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ar.id, ar.job_id, ar.asset_url_id, ar.source_page_url_id, ar.reference_type,
		       ar.natural_width, ar.natural_height, ar.rendered_width, ar.rendered_height,
		       au.normalized_url, pu.normalized_url
		FROM asset_references ar
		JOIN urls au ON au.id = ar.asset_url_id
		JOIN urls pu ON pu.id = ar.source_page_url_id
		WHERE ar.job_id = ? ORDER BY ar.id ASC LIMIT ?`, jobID, reportAssetReferencePreviewLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id, assetURLID, srcURLID        int64
			jid, refType, assetURL, pageURL string
			naturalWidth, naturalHeight     sql.NullInt64
			renderedWidth, renderedHeight   sql.NullInt64
		)
		if err := rows.Scan(&id, &jid, &assetURLID, &srcURLID, &refType, &naturalWidth, &naturalHeight, &renderedWidth, &renderedHeight, &assetURL, &pageURL); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":                 id,
			"job_id":             jid,
			"asset_url_id":       assetURLID,
			"source_page_url_id": srcURLID,
			"reference_type":     refType,
			"natural_width":      nullInt64(naturalWidth),
			"natural_height":     nullInt64(naturalHeight),
			"rendered_width":     nullInt64(renderedWidth),
			"rendered_height":    nullInt64(renderedHeight),
			"asset_url":          assetURL,
			"page_url":           pageURL,
		})
	}
	return out, rows.Err()
}

// loadEdges returns (internalEdges, externalLinks, error).
// internal_edges uses snake_case (raw shape); external_links uses camelCase
// (legacy DTO shape) — this matches what the UI expects in each section.
func loadEdges(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, []map[string]any, int, int, error) {
	var internalTotal, externalTotal int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM edges
		WHERE job_id = ? AND is_internal = 1 AND relation_type = 'link'`, jobID).Scan(&internalTotal); err != nil {
		return nil, nil, 0, 0, err
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM edges
		WHERE job_id = ? AND is_internal = 0 AND relation_type = 'link'`, jobID).Scan(&externalTotal); err != nil {
		return nil, nil, 0, 0, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT e.id, e.job_id, e.source_url_id, e.normalized_target_url_id,
		       e.source_kind, e.relation_type, e.rel_flags_json, e.discovery_mode,
		       e.anchor_text, e.is_internal, e.declared_target_url,
		       e.final_target_url_id,
		       COALESCE(e.target_status_code, (
		         SELECT f.status_code FROM fetches f
		         WHERE f.job_id = e.job_id
		           AND f.requested_url_id = COALESCE(e.final_target_url_id, e.normalized_target_url_id)
		         ORDER BY f.id DESC LIMIT 1
		       )) AS target_status_code,
		       su.normalized_url AS source_url,
		       tu.normalized_url AS normalized_target_url,
		       fu.normalized_url AS final_target_url
		FROM edges e
		JOIN urls su ON su.id = e.source_url_id
		LEFT JOIN urls tu ON tu.id = e.normalized_target_url_id
		LEFT JOIN urls fu ON fu.id = e.final_target_url_id
		WHERE e.job_id = ? AND e.relation_type = 'link'
		ORDER BY e.id ASC LIMIT ?`, jobID, reportEdgePreviewLimit)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	defer rows.Close()

	internal := []map[string]any{}
	external := []map[string]any{}
	for rows.Next() {
		var (
			id, srcURLID                      int64
			jid, sk, rt, dm, declared, srcURL string
			targetURL, finalURL               sql.NullString
			normTargetID, finalTargetID       sql.NullInt64
			relFlags, anchor                  sql.NullString
			isInternal                        int64
			targetStatus                      sql.NullInt64
		)
		if err := rows.Scan(&id, &jid, &srcURLID, &normTargetID, &sk, &rt, &relFlags, &dm,
			&anchor, &isInternal, &declared, &finalTargetID, &targetStatus, &srcURL, &targetURL, &finalURL); err != nil {
			return nil, nil, 0, 0, err
		}
		if isInternal == 1 {
			internal = append(internal, map[string]any{
				"id":                       id,
				"job_id":                   jid,
				"source_url_id":            srcURLID,
				"normalized_target_url_id": nullInt64(normTargetID),
				"source_kind":              sk,
				"relation_type":            rt,
				"rel_flags_json":           nullString(relFlags),
				"discovery_mode":           dm,
				"anchor_text":              nullString(anchor),
				"is_internal":              isInternal,
				"declared_target_url":      declared,
				"final_target_url_id":      nullInt64(finalTargetID),
				"target_status_code":       nullInt64(targetStatus),
				"source_url":               srcURL,
				"target_url":               nullString(targetURL),
				"final_target_url":         nullString(finalURL),
			})
		} else {
			// camelCase shape — matches the legacy DTO the Links section was built against.
			external = append(external, map[string]any{
				"id":                id,
				"sourceUrl":         srcURL,
				"targetUrl":         declared,
				"sourceKind":        sk,
				"relationType":      rt,
				"relFlagsJson":      nullString(relFlags),
				"discoveryMode":     dm,
				"anchorText":        nullString(anchor),
				"isInternal":        false,
				"declaredTargetUrl": declared,
			})
		}
	}
	return internal, external, internalTotal, externalTotal, rows.Err()
}

func loadRedirectHops(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, fetch_id, hop_index, status_code, from_url, to_url
		FROM redirect_hops WHERE job_id = ? LIMIT ?`, jobID, reportExtrasLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id, fetchID, hopIdx, statusCode int64
			jid, fromURL, toURL             string
		)
		if err := rows.Scan(&id, &jid, &fetchID, &hopIdx, &statusCode, &fromURL, &toURL); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":          id,
			"job_id":      jid,
			"fetch_id":    fetchID,
			"hop_index":   hopIdx,
			"status_code": statusCode,
			"from_url":    fromURL,
			"to_url":      toURL,
		})
	}
	return out, rows.Err()
}

func loadLlmsFindings(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, host, present, raw_content, sections_json, referenced_urls_json
		FROM llms_findings WHERE job_id = ? LIMIT ?`, jobID, reportExtrasLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                int64
			jid, host                         string
			present                           int64
			rawContent, sectionsJSON, refURLs sql.NullString
		)
		if err := rows.Scan(&id, &jid, &host, &present, &rawContent, &sectionsJSON, &refURLs); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":              id,
			"job_id":          jid,
			"host":            host,
			"present":         present,
			"raw_content":     nullString(rawContent),
			"sections":        decodeJSONField(sectionsJSON),
			"referenced_urls": decodeJSONField(refURLs),
		})
	}
	return out, rows.Err()
}

// decodeJSONField decodes a stored JSON column into a generic value, or nil
// when the column is null/empty/invalid.
func decodeJSONField(ns sql.NullString) any {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(ns.String), &v); err != nil {
		return nil
	}
	return v
}

func loadResponseCodes(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT f.id, f.fetch_seq, u.normalized_url, f.status_code, f.redirect_hop_count,
		       f.ttfb_ms, f.response_body_size, f.content_type, f.content_encoding,
		       f.response_headers_json, f.http_method, f.fetch_kind, f.render_mode, f.fetched_at
		FROM fetches f JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ? ORDER BY f.id ASC LIMIT ?`, jobID, reportResponseCodePreviewLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id, fetchSeq, redirectHops            int64
			requestedURL, httpMethod, fetchKind   string
			renderMode, fetchedAt                 string
			statusCode, ttfb, bodySize            sql.NullInt64
			contentType, contentEncoding, headers sql.NullString
		)
		if err := rows.Scan(&id, &fetchSeq, &requestedURL, &statusCode, &redirectHops,
			&ttfb, &bodySize, &contentType, &contentEncoding, &headers,
			&httpMethod, &fetchKind, &renderMode, &fetchedAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":                  id,
			"fetchSeq":            fetchSeq,
			"requestedUrl":        requestedURL,
			"statusCode":          nullInt64(statusCode),
			"redirectHopCount":    redirectHops,
			"ttfbMs":              nullInt64(ttfb),
			"responseBodySize":    nullInt64(bodySize),
			"contentType":         nullString(contentType),
			"contentEncoding":     nullString(contentEncoding),
			"responseHeadersJson": nullString(headers),
			"httpMethod":          httpMethod,
			"fetchKind":           fetchKind,
			"renderMode":          renderMode,
			"fetchedAt":           fetchedAt,
		})
	}
	return out, rows.Err()
}

var securityHeaderNames = []string{
	"strict-transport-security",
	"content-security-policy",
	"x-content-type-options",
	"x-frame-options",
	"referrer-policy",
	"x-xss-protection",
	"permissions-policy",
}

func loadSecurityHeaders(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, map[string]any, error) {
	summary, err := loadSecurityHeadersSummary(ctx, db, jobID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT u.normalized_url, f.status_code, f.response_headers_json
		FROM fetches f JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ?
		ORDER BY f.id ASC LIMIT ?`, jobID, reportSecurityPreviewLimit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			urlValue    string
			statusCode  sql.NullInt64
			headersJSON sql.NullString
		)
		if err := rows.Scan(&urlValue, &statusCode, &headersJSON); err != nil {
			return nil, nil, err
		}
		headers := buildSecurityHeaderSnapshot(headersJSON)
		out = append(out, map[string]any{
			"url":        urlValue,
			"statusCode": nullInt64(statusCode),
			"headers":    headers,
		})
	}
	return out, summary, rows.Err()
}

func loadSecurityHeadersSummary(ctx context.Context, db *storage.DB, jobID string) (map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT u.normalized_url, f.response_headers_json
		FROM fetches f JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ?`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type securityCounts struct {
		present int
		missing int
	}
	worstByURL := map[string]securityCounts{}
	for rows.Next() {
		var urlValue string
		var headersJSON sql.NullString
		if err := rows.Scan(&urlValue, &headersJSON); err != nil {
			return nil, err
		}
		headers := buildSecurityHeaderSnapshot(headersJSON)
		counts := securityCounts{}
		for _, name := range securityHeaderNames {
			if present, _ := headers[name]["present"].(bool); present {
				counts.present++
			} else {
				counts.missing++
			}
		}
		if existing, ok := worstByURL[urlValue]; !ok || counts.missing > existing.missing {
			worstByURL[urlValue] = counts
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	totalPresent := 0
	totalMissing := 0
	pagesWithIssues := 0
	for _, counts := range worstByURL {
		totalPresent += counts.present
		totalMissing += counts.missing
		if counts.missing > 0 {
			pagesWithIssues++
		}
	}
	return map[string]any{
		"rows":            len(worstByURL),
		"headersPresent":  totalPresent,
		"headersMissing":  totalMissing,
		"pagesWithIssues": pagesWithIssues,
	}, nil
}

func buildSecurityHeaderSnapshot(headersJSON sql.NullString) map[string]map[string]any {
	snapshot := map[string]map[string]any{}
	for _, name := range securityHeaderNames {
		snapshot[name] = map[string]any{
			"present": false,
			"value":   "",
		}
	}
	if !headersJSON.Valid || strings.TrimSpace(headersJSON.String) == "" {
		return snapshot
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(headersJSON.String), &raw); err != nil {
		return snapshot
	}

	normalized := map[string]string{}
	for key, value := range raw {
		normalized[strings.ToLower(key)] = headerValueString(value)
	}
	for _, name := range securityHeaderNames {
		if value, ok := normalized[name]; ok {
			snapshot[name] = map[string]any{
				"present": strings.TrimSpace(value) != "",
				"value":   value,
			}
		}
	}
	return snapshot
}

func headerValueString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case []string:
		return strings.Join(v, ", ")
	default:
		return ""
	}
}

// loadEventsAndAudits reads crawl_events and splits psi/axe events into their
// own arrays (matching the legacy report shape).
func loadEventsAndAudits(ctx context.Context, db *storage.DB, jobID string) ([]map[string]any, []map[string]any, []map[string]any, []map[string]any, []map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, timestamp, event_type, details_json, url
		FROM crawl_events WHERE job_id = ? ORDER BY id ASC LIMIT ?`, jobID, reportExtrasLimit)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	defer rows.Close()

	events := []map[string]any{}
	metrics := []map[string]any{}
	psi := []map[string]any{}
	axe := []map[string]any{}
	markdownNegotiation := []map[string]any{}
	for rows.Next() {
		var (
			id                    int64
			jid, ts, eventType    string
			detailsJSON, urlValue sql.NullString
		)
		if err := rows.Scan(&id, &jid, &ts, &eventType, &detailsJSON, &urlValue); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		details := decodeJSONField(detailsJSON)
		urlPtr := nullString(urlValue)

		switch eventType {
		case "psi_audit":
			if m, ok := details.(map[string]any); ok {
				if urlPtr != nil {
					m["url"] = *urlPtr
				}
				psi = append(psi, m)
			}
		case "axe_audit":
			if m, ok := details.(map[string]any); ok {
				if urlPtr != nil {
					m["url"] = *urlPtr
				}
				axe = append(axe, m)
			}
		case "markdown_negotiation":
			if m, ok := details.(map[string]any); ok {
				m["id"] = id
				m["job_id"] = jid
				m["timestamp"] = ts
				markdownNegotiation = append(markdownNegotiation, m)
			}
		case "metric":
			if m, ok := details.(map[string]any); ok {
				m["id"] = id
				m["job_id"] = jid
				m["timestamp"] = ts
				if urlPtr != nil {
					m["url"] = *urlPtr
				}
				metrics = append(metrics, m)
			}
		default:
			events = append(events, map[string]any{
				"id":         id,
				"job_id":     jid,
				"timestamp":  ts,
				"event_type": eventType,
				"url":        urlPtr,
				"details":    details,
			})
		}
	}
	return events, metrics, psi, axe, markdownNegotiation, rows.Err()
}
