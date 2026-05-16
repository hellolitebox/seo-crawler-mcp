package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/dto"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// handleJobPageBundleV2 handles GET /api/jobs/{id}/page?url=...
//
// Returns a per-page bundle the report UI uses to render a single page's
// drawer (issues, headings, links, images, performance, accessibility,
// security, sitemap, agent). The /report endpoint stops embedding all of
// this for every page beyond a certain crawl size — that produced ~23MB
// initial payloads for 100-page crawls. This endpoint is the lazy fallback
// the drawer fetches on click.
func (s *Server) handleJobPageBundleV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobPageBundle(w, r, r.PathValue("id"))
}

func (s *Server) handleJobPageBundle(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	if _, err := s.db.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}

	pageURL := r.URL.Query().Get("url")
	if pageURL == "" {
		writeError(w, http.StatusBadRequest, "url query param is required")
		return
	}

	urlRow, err := s.db.GetURLByNormalized(jobID, pageURL)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("url %q not found in job %q", pageURL, jobID))
		return
	}

	lookup := s.urlLookup()

	// Page row — may legitimately be missing (URL discovered but never crawled).
	var pagePayload any
	if p, err := s.db.GetPageByURL(jobID, urlRow.ID); err == nil {
		pagePayload = dto.PageFromStorage(*p, lookup)
	}

	issues := []dto.IssueDTO{}
	if storeIssues, err := s.db.GetIssuesByURL(jobID, urlRow.ID); err == nil {
		for _, i := range storeIssues {
			issues = append(issues, dto.IssueFromStorage(i, lookup))
		}
	}

	ctx := r.Context()

	inboundEdges, outboundEdges, outboundExternal := loadPageEdges(ctx, s.db, jobID, urlRow.ID)
	assetRefs, assets := loadPageAssets(ctx, s.db, jobID, urlRow.ID)
	redirectHops := loadPageRedirectHops(ctx, s.db, jobID, urlRow.NormalizedURL)
	psi, axe := loadPagePSIAxe(ctx, s.db, jobID, urlRow.NormalizedURL)
	sitemapEntries := loadPageSitemapEntries(ctx, s.db, jobID, urlRow.NormalizedURL)
	llms := loadPageLlmsFindings(ctx, s.db, jobID, urlRow.Host)
	responseCodes := loadPageResponseCodes(ctx, s.db, jobID, urlRow.ID)
	security := loadPageSecurityHeaders(ctx, s.db, jobID, urlRow.ID)
	mdNeg := loadPageMarkdownNegotiation(ctx, s.db, jobID, urlRow.NormalizedURL)

	writeJSON(w, http.StatusOK, map[string]any{
		"url":                  urlRow.NormalizedURL,
		"page":                 pagePayload,
		"issues":               issues,
		"inbound_edges":        inboundEdges,
		"outbound_edges":       outboundEdges,
		"outbound_external":    outboundExternal,
		"asset_references":     assetRefs,
		"assets":               assets,
		"redirect_hops":        redirectHops,
		"psi_audits":           psi,
		"axe_audits":           axe,
		"sitemap_entries":      sitemapEntries,
		"llms_findings":        llms,
		"response_codes":       responseCodes,
		"security":             security,
		"markdown_negotiation": mdNeg,
	})
}

// loadPageMarkdownNegotiation pulls the per-page entry from the
// job-level markdown_negotiation event, returning nil when no record exists.
func loadPageMarkdownNegotiation(ctx context.Context, db *storage.DB, jobID, pageURL string) any {
	rows, err := db.QueryContext(ctx, `
		SELECT details_json FROM crawl_events
		WHERE job_id = ? AND event_type = 'markdown_negotiation'
		ORDER BY id DESC LIMIT 1`,
		jobID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	if !rows.Next() {
		return nil
	}
	var details sql.NullString
	if err := rows.Scan(&details); err != nil {
		return nil
	}
	parsed, ok := decodeJSONField(details).(map[string]any)
	if !ok {
		return nil
	}
	pages, ok := parsed["pages"].([]any)
	if !ok {
		return nil
	}
	for _, p := range pages {
		entry, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if u, _ := entry["url"].(string); u == pageURL {
			return entry
		}
	}
	return nil
}

// loadPageEdges returns (inboundInternal, outboundInternal, outboundExternal).
// Inbound = edges where this URL is the resolved (or declared) target.
// Outbound = edges where this URL is the source.
func loadPageEdges(ctx context.Context, db *storage.DB, jobID string, urlID int64) ([]map[string]any, []map[string]any, []map[string]any) {
	rows, err := db.QueryContext(ctx, `
		SELECT e.id, e.job_id, e.source_url_id, e.normalized_target_url_id,
		       e.source_kind, e.relation_type, e.rel_flags_json, e.discovery_mode,
		       e.anchor_text, e.is_internal, e.declared_target_url,
		       e.final_target_url_id, e.target_status_code,
		       su.normalized_url AS source_url
		FROM edges e JOIN urls su ON su.id = e.source_url_id
		WHERE e.job_id = ?
		  AND (e.source_url_id = ?
		       OR e.normalized_target_url_id = ?
		       OR e.final_target_url_id = ?)`,
		jobID, urlID, urlID, urlID)
	if err != nil {
		return []map[string]any{}, []map[string]any{}, []map[string]any{}
	}
	defer rows.Close()

	inbound := []map[string]any{}
	outbound := []map[string]any{}
	external := []map[string]any{}
	for rows.Next() {
		var (
			id, srcURLID                      int64
			jid, sk, rt, dm, declared, srcURL string
			normTargetID, finalTargetID       sql.NullInt64
			relFlags, anchor                  sql.NullString
			isInternal                        int64
			targetStatus                      sql.NullInt64
		)
		if err := rows.Scan(&id, &jid, &srcURLID, &normTargetID, &sk, &rt, &relFlags, &dm,
			&anchor, &isInternal, &declared, &finalTargetID, &targetStatus, &srcURL); err != nil {
			continue
		}

		if isInternal == 1 {
			row := map[string]any{
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
			}
			if srcURLID == urlID {
				outbound = append(outbound, row)
			} else {
				inbound = append(inbound, row)
			}
		} else if srcURLID == urlID {
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
	return inbound, outbound, external
}

// loadPageAssets returns (assetReferences, assets), filtered to assets
// referenced from this page.
func loadPageAssets(ctx context.Context, db *storage.DB, jobID string, pageURLID int64) ([]map[string]any, []map[string]any) {
	rows, err := db.QueryContext(ctx, `
		SELECT ar.id, ar.job_id, ar.asset_url_id, ar.source_page_url_id, ar.reference_type,
		       ar.natural_width, ar.natural_height, ar.rendered_width, ar.rendered_height,
		       au.normalized_url, pu.normalized_url
		FROM asset_references ar
		JOIN urls au ON au.id = ar.asset_url_id
		JOIN urls pu ON pu.id = ar.source_page_url_id
		WHERE ar.job_id = ? AND ar.source_page_url_id = ?`,
		jobID, pageURLID)
	if err != nil {
		return []map[string]any{}, []map[string]any{}
	}
	defer rows.Close()

	refs := []map[string]any{}
	assetURLIDs := map[int64]struct{}{}
	for rows.Next() {
		var (
			id, assetURLID, srcURLID        int64
			jid, refType, assetURL, pageURL string
			naturalWidth, naturalHeight     sql.NullInt64
			renderedWidth, renderedHeight   sql.NullInt64
		)
		if err := rows.Scan(&id, &jid, &assetURLID, &srcURLID, &refType, &naturalWidth, &naturalHeight, &renderedWidth, &renderedHeight, &assetURL, &pageURL); err != nil {
			continue
		}
		refs = append(refs, map[string]any{
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
		assetURLIDs[assetURLID] = struct{}{}
	}

	if len(assetURLIDs) == 0 {
		return refs, []map[string]any{}
	}

	ids := make([]int64, 0, len(assetURLIDs))
	for id := range assetURLIDs {
		ids = append(ids, id)
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+1)
	args = append(args, jobID)
	for _, id := range ids {
		args = append(args, id)
	}

	assetRows, err := db.QueryContext(ctx, `
		SELECT a.id, a.job_id, a.url_id, a.content_type, a.content_encoding,
		       a.cache_control, a.transfer_size, a.decoded_size,
		       a.status_code, a.content_length,
		       u.normalized_url
		FROM assets a JOIN urls u ON u.id = a.url_id
		WHERE a.job_id = ? AND a.url_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return refs, []map[string]any{}
	}
	defer assetRows.Close()

	assets := []map[string]any{}
	for assetRows.Next() {
		var (
			id, urlID                                  int64
			jid, urlStr                                string
			contentType, contentEncoding, cacheControl sql.NullString
			transferSize, decodedSize                  sql.NullInt64
			statusCode, contentLength                  sql.NullInt64
		)
		if err := assetRows.Scan(&id, &jid, &urlID, &contentType, &contentEncoding, &cacheControl, &transferSize, &decodedSize, &statusCode, &contentLength, &urlStr); err != nil {
			continue
		}
		assets = append(assets, map[string]any{
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
	return refs, assets
}

func loadPageRedirectHops(ctx context.Context, db *storage.DB, jobID, pageURL string) []map[string]any {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, fetch_id, hop_index, status_code, from_url, to_url
		FROM redirect_hops
		WHERE job_id = ? AND (from_url = ? OR to_url = ?)`,
		jobID, pageURL, pageURL)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id, fetchID, hopIdx, statusCode int64
			jid, fromURL, toURL             string
		)
		if err := rows.Scan(&id, &jid, &fetchID, &hopIdx, &statusCode, &fromURL, &toURL); err != nil {
			continue
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
	return out
}

func loadPagePSIAxe(ctx context.Context, db *storage.DB, jobID, pageURL string) ([]map[string]any, []map[string]any) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, timestamp, event_type, details_json, url
		FROM crawl_events
		WHERE job_id = ? AND url = ? AND event_type IN ('psi_audit', 'axe_audit')
		ORDER BY id ASC`,
		jobID, pageURL)
	if err != nil {
		return []map[string]any{}, []map[string]any{}
	}
	defer rows.Close()

	psi := []map[string]any{}
	axe := []map[string]any{}
	for rows.Next() {
		var (
			id                    int64
			jid, ts, eventType    string
			detailsJSON, urlValue sql.NullString
		)
		if err := rows.Scan(&id, &jid, &ts, &eventType, &detailsJSON, &urlValue); err != nil {
			continue
		}
		details := decodeJSONField(detailsJSON)
		urlPtr := nullString(urlValue)
		m, ok := details.(map[string]any)
		if !ok {
			continue
		}
		if urlPtr != nil {
			m["url"] = *urlPtr
		}
		switch eventType {
		case "psi_audit":
			psi = append(psi, m)
		case "axe_audit":
			axe = append(axe, m)
		}
	}
	return psi, axe
}

func loadPageSitemapEntries(ctx context.Context, db *storage.DB, jobID, pageURL string) []map[string]any {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, url, source_sitemap_url, source_host,
		       lastmod, changefreq, priority, reconciliation_status
		FROM sitemap_entries
		WHERE job_id = ? AND url = ?`,
		jobID, pageURL)
	if err != nil {
		return []map[string]any{}
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
			continue
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
	return out
}

func loadPageLlmsFindings(ctx context.Context, db *storage.DB, jobID, host string) []map[string]any {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_id, host, present, raw_content, sections_json, referenced_urls_json
		FROM llms_findings
		WHERE job_id = ? AND host = ?`,
		jobID, host)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                int64
			jid, h                            string
			present                           int64
			rawContent, sectionsJSON, refURLs sql.NullString
		)
		if err := rows.Scan(&id, &jid, &h, &present, &rawContent, &sectionsJSON, &refURLs); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id":              id,
			"job_id":          jid,
			"host":            h,
			"present":         present,
			"raw_content":     nullString(rawContent),
			"sections":        decodeJSONField(sectionsJSON),
			"referenced_urls": decodeJSONField(refURLs),
		})
	}
	return out
}

func loadPageSecurityHeaders(ctx context.Context, db *storage.DB, jobID string, urlID int64) []map[string]any {
	rows, err := db.QueryContext(ctx, `
		SELECT u.normalized_url, f.status_code, f.response_headers_json
		FROM fetches f JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ? AND f.requested_url_id = ?
		ORDER BY f.id ASC`,
		jobID, urlID)
	if err != nil {
		return []map[string]any{}
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
			continue
		}
		out = append(out, map[string]any{
			"url":        urlValue,
			"statusCode": nullInt64(statusCode),
			"headers":    buildSecurityHeaderSnapshot(headersJSON),
		})
	}
	return out
}

func loadPageResponseCodes(ctx context.Context, db *storage.DB, jobID string, urlID int64) []map[string]any {
	rows, err := db.QueryContext(ctx, `
		SELECT f.id, f.fetch_seq, u.normalized_url, f.status_code, f.redirect_hop_count,
		       f.ttfb_ms, f.response_body_size, f.content_type, f.content_encoding,
		       f.response_headers_json, f.http_method, f.fetch_kind, f.render_mode, f.fetched_at
		FROM fetches f JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ? AND f.requested_url_id = ?
		ORDER BY f.id ASC`,
		jobID, urlID)
	if err != nil {
		return []map[string]any{}
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
			continue
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
	return out
}
