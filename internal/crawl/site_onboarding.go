// Package crawl provides crawl orchestration components.
package crawl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/llmstxt"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/robots"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/sitemap"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// HostOnboarder performs per-host discovery (robots.txt, sitemaps, llms.txt)
// exactly once per host during a crawl.
type HostOnboarder struct {
	fetcher                 *fetcher.Fetcher
	httpClient              *http.Client // uses fetcher's SSRF-protected transport
	db                      *storage.DB
	sitemapMax              int
	userAgent               string
	robotsUnreachablePolicy string // "allow", "disallow", "cache_then_allow"
}

// HostInfo holds everything discovered during host onboarding.
type HostInfo struct {
	Host           string
	RobotsFile     *robots.RobotsFile
	RobotsRaw      string
	CrawlDelay     time.Duration
	SitemapURLs    []string
	SitemapEntries []sitemap.Entry
	LlmsTxtFound   bool
	LlmsTxtRaw     string
	LlmsTxt        *llmstxt.LlmsTxt
	Events         []string
}

// NewHostOnboarder creates a HostOnboarder.
func NewHostOnboarder(f *fetcher.Fetcher, db *storage.DB, sitemapMax int, userAgent string) *HostOnboarder {
	return &HostOnboarder{
		fetcher:                 f,
		httpClient:              f.SafeClient(),
		db:                      db,
		sitemapMax:              sitemapMax,
		userAgent:               userAgent,
		robotsUnreachablePolicy: "allow",
	}
}

// NewHostOnboarderWithPolicy creates a HostOnboarder with a robots unreachable policy.
func NewHostOnboarderWithPolicy(f *fetcher.Fetcher, db *storage.DB, sitemapMax int, userAgent string, robotsPolicy string) *HostOnboarder {
	if robotsPolicy == "" {
		robotsPolicy = "allow"
	}
	return &HostOnboarder{
		fetcher:                 f,
		httpClient:              f.SafeClient(),
		db:                      db,
		sitemapMax:              sitemapMax,
		userAgent:               userAgent,
		robotsUnreachablePolicy: robotsPolicy,
	}
}

// OnboardHost performs all discovery for a host. Safe to call concurrently.
func (h *HostOnboarder) OnboardHost(ctx context.Context, jobID, host, scheme string) (*HostInfo, error) {
	info := &HostInfo{
		Host:           host,
		SitemapURLs:    []string{},
		SitemapEntries: []sitemap.Entry{},
		Events:         []string{},
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("onboarding host %q: %w", host, err)
	}

	h.discoverRobots(ctx, jobID, host, scheme, info)
	if err := ctx.Err(); err != nil {
		return info, err
	}
	h.discoverSitemaps(ctx, jobID, host, scheme, info)
	if err := ctx.Err(); err != nil {
		return info, err
	}
	h.discoverLlmsTxt(ctx, jobID, host, scheme, info)

	return info, nil
}

// discoverRobots fetches and parses robots.txt for the host.
func (h *HostOnboarder) discoverRobots(ctx context.Context, jobID, host, scheme string, info *HostInfo) {
	robotsURL := fmt.Sprintf("%s://%s/robots.txt", scheme, host)
	result, err := h.fetcher.FetchContext(ctx, robotsURL)
	if err != nil {
		// Apply robotsUnreachablePolicy for fetch errors (timeout, DNS, etc.)
		h.applyRobotsUnreachablePolicy(host, fmt.Sprintf("robots.txt fetch error for %q: %v", host, err), info)
		return
	}

	// Check context between network call and processing.
	if ctx.Err() != nil {
		return
	}

	if result.StatusCode >= 500 {
		// Retry once for server errors
		result2, err2 := h.fetcher.FetchContext(ctx, robotsURL)
		if err2 != nil || (result2 != nil && result2.StatusCode >= 500) {
			h.applyRobotsUnreachablePolicy(host, fmt.Sprintf("robots.txt server error (%d) for %q after retry", result.StatusCode, host), info)
			return
		}
		// Retry succeeded — use retry result
		result = result2
		if result.StatusCode >= 400 {
			info.Events = append(info.Events, fmt.Sprintf("robots.txt not found (%d) for %q after retry — allowing all", result.StatusCode, host))
			return
		}
	}

	if result.StatusCode >= 400 {
		info.Events = append(info.Events, fmt.Sprintf("robots.txt not found (%d) for %q — allowing all", result.StatusCode, host))
		return
	}

	body := string(result.Body)
	info.RobotsRaw = body

	rf, parseErr := robots.Parse(body)
	if parseErr != nil {
		info.Events = append(info.Events, fmt.Sprintf("robots.txt parse error for %q: %v", host, parseErr))
		return
	}

	info.RobotsFile = rf

	// Extract crawl-delay.
	delay := rf.CrawlDelay(h.userAgent)
	if delay > 0 {
		info.CrawlDelay = time.Duration(delay) * time.Second
	}

	// Collect sitemap URLs from robots.txt.
	info.SitemapURLs = append(info.SitemapURLs, rf.Sitemaps...)

	// Store directives in DB.
	h.storeRobotsDirectives(jobID, host, robotsURL, rf, info)
}

// applyRobotsUnreachablePolicy applies the configured policy when robots.txt is unreachable.
func (h *HostOnboarder) applyRobotsUnreachablePolicy(host, reason string, info *HostInfo) {
	switch h.robotsUnreachablePolicy {
	case "disallow":
		info.Events = append(info.Events, fmt.Sprintf("%s — policy=disallow, blocking all paths", reason))
		// Create a robots file that disallows everything.
		disallowAll := "User-agent: *\nDisallow: /\n"
		rf, _ := robots.Parse(disallowAll)
		info.RobotsFile = rf
		info.RobotsRaw = disallowAll
	case "cache_then_allow":
		// Cache not implemented in v1 — fall through to allow.
		info.Events = append(info.Events, fmt.Sprintf("%s — policy=cache_then_allow (no cache, falling back to allow-all)", reason))
	default: // "allow"
		info.Events = append(info.Events, fmt.Sprintf("%s — policy=allow, defaulting to allow-all", reason))
	}
}

// storeRobotsDirectives persists parsed robots.txt directives.
func (h *HostOnboarder) storeRobotsDirectives(jobID, host, sourceURL string, rf *robots.RobotsFile, info *HostInfo) {
	if h.db == nil {
		return
	}
	for _, d := range rf.Rules {
		if _, err := h.db.InsertRobotsDirective(storage.RobotsDirectiveInput{
			JobID:       jobID,
			Host:        host,
			UserAgent:   d.UserAgent,
			RuleType:    d.RuleType,
			PathPattern: d.PathPattern,
			SourceURL:   sourceURL,
		}); err != nil {
			info.Events = append(info.Events, fmt.Sprintf("warning: failed to store robots directive: %v", err))
		}
	}
}

// discoverSitemaps fetches and parses sitemaps for the host.
func (h *HostOnboarder) discoverSitemaps(ctx context.Context, jobID, host, scheme string, info *HostInfo) {
	// If robots.txt didn't provide sitemaps, try fallback locations.
	if len(info.SitemapURLs) == 0 {
		info.SitemapURLs = []string{
			fmt.Sprintf("%s://%s/sitemap.xml", scheme, host),
			fmt.Sprintf("%s://%s/sitemap_index.xml", scheme, host),
		}
		info.Events = append(info.Events, fmt.Sprintf("no sitemaps in robots.txt for %q — trying fallback locations", host))
	}

	allEntries := make([]sitemap.Entry, 0)

	for _, sitemapURL := range info.SitemapURLs {
		if ctx.Err() != nil {
			return
		}

		remaining := h.sitemapMax - len(allEntries)
		entries, _, err := sitemap.FetchAndParseContext(ctx, sitemapURL, remaining, h.httpClient)
		if err != nil {
			info.Events = append(info.Events, fmt.Sprintf("sitemap fetch/parse error for %q: %v", sitemapURL, err))
			continue
		}
		allEntries = append(allEntries, entries...)
		if len(allEntries) >= h.sitemapMax {
			break
		}
	}

	info.SitemapEntries = allEntries

	// Store entries in DB.
	if h.db == nil {
		return
	}
	for _, e := range allEntries {
		input := storage.SitemapEntryInput{
			JobID:            jobID,
			URL:              e.Loc,
			SourceSitemapURL: e.SourceURL,
			SourceHost:       host,
		}
		if e.Lastmod != "" {
			input.Lastmod = &e.Lastmod
		}
		if e.Changefreq != "" {
			input.Changefreq = &e.Changefreq
		}
		if e.Priority != 0 {
			input.Priority = &e.Priority
		}
		if _, err := h.db.InsertSitemapEntry(input); err != nil {
			info.Events = append(info.Events, fmt.Sprintf("warning: failed to store sitemap entry %q: %v", e.Loc, err))
		}
	}
}

// discoverLlmsTxt fetches and parses llms.txt for the host.
func (h *HostOnboarder) discoverLlmsTxt(ctx context.Context, jobID, host, scheme string, info *HostInfo) {
	llmsURL := fmt.Sprintf("%s://%s/llms.txt", scheme, host)
	result, err := h.fetcher.FetchContext(ctx, llmsURL)

	if err != nil || result.StatusCode != 200 {
		info.LlmsTxtFound = false
		info.Events = append(info.Events, fmt.Sprintf("llms.txt not found for %q", host))
		h.storeLlmsFinding(jobID, host, false, "", nil, info)
		return
	}

	// Check context between network call and processing.
	if ctx.Err() != nil {
		return
	}

	body := string(result.Body)
	info.LlmsTxtFound = true
	info.LlmsTxtRaw = body
	info.LlmsTxt = llmstxt.Parse(body)
	info.Events = append(info.Events, fmt.Sprintf("llms.txt found for %q (%d bytes)", host, len(body)))

	h.storeLlmsFinding(jobID, host, true, body, info.LlmsTxt, info)
}

// storeLlmsFinding persists an llms.txt finding.
func (h *HostOnboarder) storeLlmsFinding(jobID, host string, present bool, raw string, parsed *llmstxt.LlmsTxt, info *HostInfo) {
	if h.db == nil {
		return
	}

	input := storage.LlmsFindingInput{
		JobID:   jobID,
		Host:    host,
		Present: present,
	}

	if present {
		input.RawContent = &raw
		if parsed != nil {
			sectionsJSON, _ := json.Marshal(parsed.Sections)
			s := string(sectionsJSON)
			input.SectionsJSON = &s

			urlsJSON, _ := json.Marshal(parsed.URLs)
			u := string(urlsJSON)
			input.ReferencedURLsJSON = &u
		}
	}

	if _, err := h.db.UpsertLlmsFinding(input); err != nil {
		info.Events = append(info.Events, fmt.Sprintf("warning: failed to store llms.txt finding for %q: %v", host, err))
	}
}
