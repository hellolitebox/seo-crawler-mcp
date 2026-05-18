package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

const aiSummaryEventType = "ai_summary_v1"

type aiSummaryPayload struct {
	Model             string              `json:"model"`
	GeneratedAt       string              `json:"generatedAt"`
	Cached            bool                `json:"cached"`
	OverallHealth     string              `json:"overallHealth"`
	ExecutiveSummary  string              `json:"executiveSummary"`
	PriorityFindings  []aiPriorityFinding `json:"priorityFindings"`
	QuickWins         []string            `json:"quickWins"`
	StrategicActions  []string            `json:"strategicActions"`
	Caveats           []string            `json:"caveats"`
	SourceFingerprint string              `json:"sourceFingerprint"`
}

type aiPriorityFinding struct {
	Severity       string `json:"severity"`
	Title          string `json:"title"`
	Evidence       string `json:"evidence"`
	WhyItMatters   string `json:"whyItMatters"`
	Recommendation string `json:"recommendation"`
}

func (s *Server) handleJobAISummaryCacheV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobAISummaryCache(w, r, r.PathValue("id"))
}

func (s *Server) handleJobAISummaryGenerateV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobAISummaryGenerate(w, r, r.PathValue("id"))
}

func (s *Server) handleJobAISummaryCache(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	if _, err := s.db.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}
	payload, ok, err := s.loadCachedAISummary(jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("loading AI summary: %v", err))
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "AI summary has not been generated for this job")
		return
	}
	payload.Cached = true
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleJobAISummaryGenerate(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	job, err := s.db.GetJob(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}
	if job.Status != "completed" {
		writeError(w, http.StatusConflict, "AI summary is available after the crawl completes")
		return
	}

	refresh := r.URL.Query().Get("refresh") == "1" || strings.EqualFold(r.URL.Query().Get("refresh"), "true")
	if !refresh {
		if payload, ok, cacheErr := s.loadCachedAISummary(jobID); cacheErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("loading AI summary: %v", cacheErr))
			return
		} else if ok {
			payload.Cached = true
			writeJSON(w, http.StatusOK, payload)
			return
		}
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		writeError(w, http.StatusNotImplemented, "OPENAI_API_KEY is not configured")
		return
	}

	input, err := s.buildAISummaryInput(r.Context(), job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("building AI summary input: %v", err))
		return
	}

	model := strings.TrimSpace(os.Getenv("SEO_CRAWLER_AI_MODEL"))
	if model == "" {
		model = "gpt-5.5"
	}

	payload, err := generateAISummaryWithOpenAI(r.Context(), apiKey, model, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("generating AI summary: %v", err))
		return
	}
	payload.Model = model
	payload.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	payload.Cached = false
	payload.SourceFingerprint = aiSummarySourceFingerprint(job)

	details, _ := json.Marshal(payload)
	detailsStr := string(details)
	if _, err := s.db.InsertEvent(jobID, aiSummaryEventType, &detailsStr, nil); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("caching AI summary: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) loadCachedAISummary(jobID string) (aiSummaryPayload, bool, error) {
	var raw sql.NullString
	err := s.db.QueryRow(
		`SELECT details_json FROM crawl_events WHERE job_id = ? AND event_type = ? ORDER BY id DESC LIMIT 1`,
		jobID, aiSummaryEventType,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return aiSummaryPayload{}, false, nil
	}
	if err != nil {
		return aiSummaryPayload{}, false, err
	}
	if !raw.Valid || raw.String == "" {
		return aiSummaryPayload{}, false, nil
	}
	var payload aiSummaryPayload
	if err := json.Unmarshal([]byte(raw.String), &payload); err != nil {
		return aiSummaryPayload{}, false, err
	}
	return payload, true, nil
}

func (s *Server) buildAISummaryInput(ctx context.Context, job *storage.CrawlJob) (map[string]any, error) {
	summary, err := s.db.GetCrawlSummary(job.ID)
	if err != nil {
		return nil, err
	}
	extras := loadReportExtras(ctx, s.db, job.ID)
	issueExamples, err := s.issueExamples(job.ID, 40)
	if err != nil {
		return nil, err
	}
	pageExamples, err := s.pageExamples(job.ID, 30)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"job": map[string]any{
			"id":             job.ID,
			"status":         job.Status,
			"createdAt":      job.CreatedAt,
			"startedAt":      nullString(job.StartedAt),
			"finishedAt":     nullString(job.FinishedAt),
			"htmlFetches":    job.PagesCrawled,
			"urlsDiscovered": job.URLsDiscovered,
			"issuesFound":    job.IssuesFound,
			"seedUrls":       decodeJSONField(sql.NullString{String: job.SeedURLs, Valid: job.SeedURLs != ""}),
		},
		"summary":              compactCrawlSummary(summary),
		"sitemapSummary":       compactAIValue(extras["sitemap_summary"], 12),
		"securitySummary":      compactAIValue(extras["security_summary"], 12),
		"urlClusterSummary":    compactAIValue(extras["url_cluster_summary"], 12),
		"coverage":             aiSummaryCoverage(extras),
		"responseCodes":        compactAIValue(compactList(extras["response_codes"], 25), 8),
		"topIssueExamples":     compactAIValue(summary.TopIssues, 10),
		"issueExamples":        issueExamples,
		"pageExamples":         pageExamples,
		"agentReadiness":       summarizeStatusRows(extras["agent_readiness"]),
		"securityFindings":     summarizeStatusRows(extras["security"]),
		"psiAudits":            summarizePSIAudits(extras["psi_audits"]),
		"axeAudits":            summarizeAxeAudits(extras["axe_audits"]),
		"markdownNegotiation":  compactAIValue(compactList(extras["markdown_negotiation"], 5), 5),
		"llmsFindings":         compactAIValue(compactList(extras["llms_findings"], 10), 5),
		"assetSamples":         compactAIValue(compactList(extras["assets"], 10), 5),
		"externalLinkSamples":  compactAIValue(compactList(extras["external_links"], 10), 5),
		"redirectHopSamples":   compactAIValue(compactList(extras["redirect_hops"], 10), 5),
		"urlVariantIssues":     compactAIValue(compactList(extras["url_variant_issues"], 10), 5),
		"sourceFingerprint":    aiSummarySourceFingerprint(job),
		"instructionsForModel": "Use only this data. Do not invent issue counts, URLs, or audits.",
	}, nil
}

func compactCrawlSummary(summary *storage.CrawlSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	return map[string]any{
		"totalPages":               summary.TotalPages,
		"totalUrls":                summary.TotalURLs,
		"totalIssues":              summary.TotalIssues,
		"issuesByType":             summary.IssuesByType,
		"issuesBySeverity":         summary.IssuesBySeverity,
		"indexabilityDistribution": summary.IndexabilityDistribution,
		"statusCodeDistribution":   summary.StatusCodeDistribution,
		"depthDistribution":        summary.DepthDistribution,
		"avgTtfb":                  summary.AvgTTFB,
		"medianTtfb":               summary.MedianTTFB,
		"p95Ttfb":                  summary.P95TTFB,
		"avgWordCount":             summary.AvgWordCount,
		"pagesWithStructuredData":  summary.PagesWithStructuredData,
		"pagesWithIssues":          summary.PagesWithIssues,
		"pagesInSitemap":           summary.PagesInSitemap,
		"orphanPageCount":          summary.OrphanPageCount,
		"duplicateContentCount":    summary.DuplicateContentCount,
		"thinContentCount":         summary.ThinContentCount,
		"crawlDuration":            summary.CrawlDuration,
		"pagesPerSecond":           summary.PagesPerSecond,
		"topIssues":                summary.TopIssues,
	}
}

func aiSummaryCoverage(extras map[string]any) map[string]any {
	return map[string]any{
		"urlsTotal":             extras["urls_total_count"],
		"internalLinksTotal":    extras["internal_edges_total_count"],
		"externalLinksTotal":    extras["external_links_total_count"],
		"assetSamples":          len(compactList(extras["assets"], 1000000)),
		"assetReferenceSamples": len(compactList(extras["asset_references"], 1000000)),
		"redirectHopSamples":    len(compactList(extras["redirect_hops"], 1000000)),
		"llmsFindingSamples":    len(compactList(extras["llms_findings"], 1000000)),
	}
}

func aiSummarySourceFingerprint(job *storage.CrawlJob) string {
	return fmt.Sprintf("%s:%d:%d:%d", job.ID, job.PagesCrawled, job.URLsDiscovered, job.IssuesFound)
}

func (s *Server) issueExamples(jobID string, limit int) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT i.issue_type, i.severity, i.scope, COALESCE(u.normalized_url, ''), i.details_json
		FROM issues i
		LEFT JOIN urls u ON u.id = i.url_id
		WHERE i.job_id = ?
		ORDER BY CASE i.severity WHEN 'error' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, i.issue_type ASC, i.id ASC
		LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var issueType, severity, scope, pageURL string
		var details sql.NullString
		if err := rows.Scan(&issueType, &severity, &scope, &pageURL, &details); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"type":     issueType,
			"severity": severity,
			"scope":    scope,
			"url":      pageURL,
			"details":  compactAIValue(decodeJSONField(details), 4),
		})
	}
	return out, rows.Err()
}

func (s *Server) pageExamples(jobID string, limit int) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT u.normalized_url, p.title, p.meta_description, p.indexability_state,
		       p.word_count, p.inbound_edge_count, p.outbound_edge_count
		FROM pages p
		JOIN urls u ON u.id = p.url_id
		WHERE p.job_id = ? AND u.is_internal = 1
		ORDER BY p.depth ASC, p.id ASC
		LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var pageURL, indexability string
		var title, desc sql.NullString
		var words sql.NullInt64
		var inbound, outbound int64
		if err := rows.Scan(&pageURL, &title, &desc, &indexability, &words, &inbound, &outbound); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"url":             pageURL,
			"title":           nullString(title),
			"metaDescription": nullString(desc),
			"indexability":    indexability,
			"wordCount":       nullInt64(words),
			"inboundLinks":    inbound,
			"outboundLinks":   outbound,
		})
	}
	return out, rows.Err()
}

func compactList(value any, limit int) []any {
	items, ok := value.([]map[string]any)
	if !ok {
		if generic, ok := value.([]any); ok {
			if len(generic) > limit {
				return generic[:limit]
			}
			return generic
		}
		return []any{}
	}
	out := make([]any, 0, min(limit, len(items)))
	for i, item := range items {
		if i >= limit {
			break
		}
		out = append(out, item)
	}
	return out
}

func compactAIValue(value any, limit int) any {
	if limit <= 0 {
		limit = 5
	}
	switch v := value.(type) {
	case nil, bool, int, int64, float64, json.Number:
		return v
	case string:
		return limitString(v, 320)
	case *string:
		if v == nil {
			return nil
		}
		return limitString(*v, 320)
	case []map[string]any:
		out := make([]any, 0, min(limit, len(v)))
		for i, item := range v {
			if i >= limit {
				break
			}
			out = append(out, compactAIValue(item, limit))
		}
		return out
	case []any:
		out := make([]any, 0, min(limit, len(v)))
		for i, item := range v {
			if i >= limit {
				break
			}
			out = append(out, compactAIValue(item, limit))
		}
		return out
	case []storage.TopIssue:
		out := make([]any, 0, min(limit, len(v)))
		for i, item := range v {
			if i >= limit {
				break
			}
			out = append(out, map[string]any{
				"type":       item.Type,
				"count":      item.Count,
				"exampleUrl": item.ExampleURL,
			})
		}
		return out
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if len(out) >= 24 {
				break
			}
			if skipAIInputKey(key) {
				continue
			}
			out[key] = compactAIValue(v[key], limit)
		}
		return out
	default:
		return limitString(fmt.Sprint(v), 320)
	}
}

func skipAIInputKey(key string) bool {
	normalized := strings.ToLower(key)
	for _, token := range []string{
		"html",
		"body",
		"content",
		"headers",
		"evidence_json",
		"resources_json",
		"raw",
		"snapshot",
		"screenshot",
		"trace",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func summarizeStatusRows(value any) map[string]any {
	rows, ok := value.([]map[string]any)
	if !ok {
		return map[string]any{"total": 0, "byStatus": map[string]int{}, "samples": []any{}}
	}
	byStatus := map[string]int{}
	samples := []any{}
	for _, row := range rows {
		status, _ := row["status"].(string)
		if status == "" {
			status = "unknown"
		}
		byStatus[status]++
		if len(samples) < 20 && (status == "fail" || status == "warning" || status == "error") {
			samples = append(samples, compactAIValue(row, 4))
		}
	}
	return map[string]any{"total": len(rows), "byStatus": byStatus, "samples": samples}
}

func summarizePSIAudits(value any) map[string]any {
	rows, ok := value.([]map[string]any)
	if !ok {
		return map[string]any{"total": 0}
	}
	type scoreSet struct{ perf, accessibility, bestPractices, seo float64 }
	var totals scoreSet
	var counts scoreSet
	samples := []any{}
	for _, row := range rows {
		for key, pair := range map[string]*float64{
			"performance":    &totals.perf,
			"accessibility":  &totals.accessibility,
			"bestPractices":  &totals.bestPractices,
			"seo":            &totals.seo,
			"best_practices": &totals.bestPractices,
		} {
			if v, ok := numericValue(row[key]); ok {
				*pair += v
				switch key {
				case "performance":
					counts.perf++
				case "accessibility":
					counts.accessibility++
				case "bestPractices", "best_practices":
					counts.bestPractices++
				case "seo":
					counts.seo++
				}
			}
		}
		if len(samples) < 8 {
			samples = append(samples, compactAIValue(row, 4))
		}
	}
	avg := func(total, count float64) any {
		if count == 0 {
			return nil
		}
		return total / count
	}
	return map[string]any{
		"total":            len(rows),
		"avgPerformance":   avg(totals.perf, counts.perf),
		"avgAccessibility": avg(totals.accessibility, counts.accessibility),
		"avgBestPractices": avg(totals.bestPractices, counts.bestPractices),
		"avgSEO":           avg(totals.seo, counts.seo),
		"samples":          samples,
	}
}

func summarizeAxeAudits(value any) map[string]any {
	rows, ok := value.([]map[string]any)
	if !ok {
		return map[string]any{"total": 0}
	}
	violations := 0
	samples := []any{}
	for _, row := range rows {
		if v, ok := numericValue(row["violationCount"]); ok {
			violations += int(v)
		} else if arr, ok := row["violations"].([]any); ok {
			violations += len(arr)
		}
		if len(samples) < 8 {
			samples = append(samples, compactAIValue(row, 4))
		}
	}
	return map[string]any{"total": len(rows), "violationCount": violations, "samples": samples}
}

func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func generateAISummaryWithOpenAI(ctx context.Context, apiKey, model string, input map[string]any) (aiSummaryPayload, error) {
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return aiSummaryPayload{}, err
	}
	prompt := aiSummaryPrompt(string(inputBytes))
	reqBody := map[string]any{
		"model": model,
		"reasoning": map[string]any{
			"effort": "low",
		},
		"input": []map[string]any{
			{
				"role":    "system",
				"content": "You are an expert technical SEO auditor. Return only valid JSON.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "seo_crawl_ai_summary",
				"strict": true,
				"schema": aiSummaryJSONSchema(),
			},
		},
		"max_output_tokens": 1600,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(bodyBytes))
	if err != nil {
		return aiSummaryPayload{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return aiSummaryPayload{}, err
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aiSummaryPayload{}, fmt.Errorf("OpenAI returned HTTP %d: %s", resp.StatusCode, limitString(string(respBytes), 500))
	}
	text, err := openAIResponseText(respBytes)
	if err != nil {
		return aiSummaryPayload{}, err
	}
	var payload aiSummaryPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return aiSummaryPayload{}, fmt.Errorf("parsing OpenAI summary JSON: %w", err)
	}
	normalizeAISummaryPayload(&payload)
	return payload, nil
}

func aiSummaryPrompt(inputJSON string) string {
	return `You are an expert technical SEO auditor analyzing a crawler report.

Return ONLY valid JSON matching this shape:
{
  "overallHealth": "critical|poor|fair|good",
  "executiveSummary": "2-4 concise sentences",
  "priorityFindings": [
    {
      "severity": "critical|high|medium|low",
      "title": "short finding title",
      "evidence": "numbers and examples from the input",
      "whyItMatters": "business/SEO impact",
      "recommendation": "specific next action"
    }
  ],
  "quickWins": ["specific fix"],
  "strategicActions": ["larger initiative"],
  "caveats": ["data limitation or sampling note"]
}

Rules:
- Use only the crawl data below. Do not invent counts, URLs, competitors, traffic, or rankings.
- Prioritize crawlability/indexation, sitemap/canonical problems, metadata duplication, thin/duplicate content, internal linking, response codes, performance, accessibility, security, and agent-readiness.
- Keep priorityFindings to 5-8 items.
- Keep quickWins and strategicActions to 3-6 items each.
- Write in direct product/SEO language for an operator, not consultant fluff.

Crawl data:
` + inputJSON
}

func aiSummaryJSONSchema() map[string]any {
	stringArray := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"overallHealth": map[string]any{
				"type": "string",
				"enum": []string{"critical", "poor", "fair", "good"},
			},
			"executiveSummary": map[string]any{"type": "string"},
			"priorityFindings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"severity": map[string]any{
							"type": "string",
							"enum": []string{"critical", "high", "medium", "low"},
						},
						"title":          map[string]any{"type": "string"},
						"evidence":       map[string]any{"type": "string"},
						"whyItMatters":   map[string]any{"type": "string"},
						"recommendation": map[string]any{"type": "string"},
					},
					"required":             []string{"severity", "title", "evidence", "whyItMatters", "recommendation"},
					"additionalProperties": false,
				},
			},
			"quickWins":        stringArray,
			"strategicActions": stringArray,
			"caveats":          stringArray,
		},
		"required":             []string{"overallHealth", "executiveSummary", "priorityFindings", "quickWins", "strategicActions", "caveats"},
		"additionalProperties": false,
	}
}

func openAIResponseText(raw []byte) (string, error) {
	var parsed struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("parsing OpenAI response: %w", err)
	}
	text := strings.TrimSpace(parsed.OutputText)
	if text == "" {
		for _, output := range parsed.Output {
			for _, content := range output.Content {
				if content.Text != "" {
					text = strings.TrimSpace(content.Text)
					break
				}
			}
			if text != "" {
				break
			}
		}
	}
	if text == "" {
		return "", fmt.Errorf("OpenAI returned no output text")
	}
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text), nil
}

func limitString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func normalizeAISummaryPayload(payload *aiSummaryPayload) {
	payload.OverallHealth = strings.ToLower(strings.TrimSpace(payload.OverallHealth))
	switch payload.OverallHealth {
	case "critical", "poor", "fair", "good":
	default:
		payload.OverallHealth = "fair"
	}
	if payload.PriorityFindings == nil {
		payload.PriorityFindings = []aiPriorityFinding{}
	}
	if payload.QuickWins == nil {
		payload.QuickWins = []string{}
	}
	if payload.StrategicActions == nil {
		payload.StrategicActions = []string{}
	}
	if payload.Caveats == nil {
		payload.Caveats = []string{}
	}
	sort.SliceStable(payload.PriorityFindings, func(i, j int) bool {
		return severityRank(payload.PriorityFindings[i].Severity) < severityRank(payload.PriorityFindings[j].Severity)
	})
}

func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}
