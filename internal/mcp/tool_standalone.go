package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/issues"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/robots"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/sitemap"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// analyzeResult is the JSON response from analyze_url.
type analyzeResult struct {
	URL    string                  `json:"url"`
	Page   *analyzePageData       `json:"page"`
	Links  []analyzeLinkData      `json:"links"`
	Issues []issues.DetectedIssue `json:"issues"`
}

type analyzePageData struct {
	Title                string `json:"title"`
	TitleLength          int    `json:"titleLength"`
	MetaDescription      string `json:"metaDescription"`
	DescriptionLength    int    `json:"descriptionLength"`
	MetaRobots           string `json:"metaRobots,omitempty"`
	IndexabilityState    string `json:"indexabilityState"`
	CanonicalURL         string `json:"canonicalUrl,omitempty"`
	WordCount            int    `json:"wordCount"`
	MainContentWordCount int    `json:"mainContentWordCount"`
	ContentHash          string `json:"contentHash"`
	JSSuspect            bool   `json:"jsSuspect"`
	H1Count              int    `json:"h1Count"`
	ImageCount           int    `json:"imageCount"`
	LinkCount            int    `json:"linkCount"`
}

type analyzeLinkData struct {
	URL        string `json:"url"`
	AnchorText string `json:"anchorText,omitempty"`
	Rel        string `json:"rel,omitempty"`
}

func (s *Server) handleAnalyzeURL(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return gomcp.NewToolResultError("parameter \"url\" is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return gomcp.NewToolResultError(fmt.Sprintf("invalid URL %q: must be http or https", rawURL)), nil
	}

	// Guard: purge expired and check concurrent analyze jobs.
	if s.db != nil {
		_, _ = s.db.PurgeExpiredAnalyzeJobs()

		count, countErr := s.db.CountActiveJobs("analyze")
		if countErr != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("checking analyze job limit: %v", countErr)), nil
		}
		maxConcurrent := 50
		if s.config != nil && s.config.MaxConcurrentAnalyze > 0 {
			maxConcurrent = s.config.MaxConcurrentAnalyze
		}
		if count >= maxConcurrent {
			return gomcp.NewToolResultError(fmt.Sprintf("concurrent analyze limit reached (%d/%d)", count, maxConcurrent)), nil
		}

		// Create mini-job with 24h TTL for tracking.
		ttl := 24 * time.Hour
		if s.config != nil && s.config.AnalyzeJobTTL > 0 {
			ttl = s.config.AnalyzeJobTTL
		}
		_, _ = s.db.CreateJobWithTTL("analyze", fmt.Sprintf(`{"url":%q}`, rawURL), "[]", ttl)
	}

	if s.fetcher == nil {
		return gomcp.NewToolResultError("server not configured: fetcher unavailable"), nil
	}

	// Fetch the URL.
	fetchResult, err := s.fetcher.Fetch(rawURL)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("fetching %q: %v", rawURL, err)), nil
	}
	if fetchResult.Error != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("fetching %q: %v", rawURL, fetchResult.Error)), nil
	}

	// Parse HTML.
	parseResult, err := parser.ParseHTML(fetchResult.Body, fetchResult.FinalURL, fetchResult.ResponseHeaders)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("parsing HTML from %q: %v", rawURL, err)), nil
	}

	// Detect issues.
	pageCtx := issues.PageContext{
		StatusCode:           fetchResult.StatusCode,
		RedirectHopCount:     len(fetchResult.RedirectHops),
		RedirectLoopDetected: fetchResult.RedirectLoopDetected,
		RedirectHopsExceeded: fetchResult.RedirectHopsExceeded,
		TTFBMS:               fetchResult.TTFBMS,
		ContentType:          fetchResult.ContentType,
		Title:                parseResult.Title,
		TitleLength:          parseResult.TitleLength,
		MetaDescription:      parseResult.MetaDescription,
		DescriptionLength:    parseResult.DescriptionLength,
		MetaRobots:           parseResult.MetaRobots,
		XRobotsTag:           parseResult.XRobotsTag,
		CanonicalType:        parseResult.CanonicalType,
		H1Count:              len(parseResult.Headings.H1),
		OGTitle:              parseResult.OpenGraph.Title,
		OGDescription:        parseResult.OpenGraph.Description,
		OGImage:              parseResult.OpenGraph.Image,
		JSONLDBlocks:         len(parseResult.JSONLDBlocks),
		JSONLDRaw:            marshalJSONLDBlocksForStandalone(parseResult.JSONLDBlocks),
		WordCount:            parseResult.ExtractedWordCount,
		MainContentWordCount: parseResult.MainContentWordCount,
		JSSuspect:            parseResult.JSSuspect,
		ScriptCount:          parseResult.ScriptCount,
		HasSPARoot:           parseResult.HasSPARoot,
	}

	// Count images without alt.
	for _, img := range parseResult.Images {
		if img.AltMissing {
			pageCtx.ImagesWithoutAlt++
		}
		if img.AltEmpty {
			pageCtx.ImagesWithEmptyAlt++
		}
	}

	detected := issues.DetectPageLocalIssues(pageCtx, issues.DefaultThresholds(), 0)

	// Build links.
	links := make([]analyzeLinkData, 0, len(parseResult.Links))
	for _, l := range parseResult.Links {
		links = append(links, analyzeLinkData{
			URL:        l.URL,
			AnchorText: l.AnchorText,
			Rel:        l.Rel,
		})
	}

	result := analyzeResult{
		URL: fetchResult.FinalURL,
		Page: &analyzePageData{
			Title:                parseResult.Title,
			TitleLength:          parseResult.TitleLength,
			MetaDescription:      parseResult.MetaDescription,
			DescriptionLength:    parseResult.DescriptionLength,
			MetaRobots:           parseResult.MetaRobots,
			IndexabilityState:    parseResult.IndexabilityState,
			CanonicalURL:         parseResult.CanonicalResolved,
			WordCount:            parseResult.ExtractedWordCount,
			MainContentWordCount: parseResult.MainContentWordCount,
			ContentHash:          parseResult.ContentHash,
			JSSuspect:            parseResult.JSSuspect,
			H1Count:              len(parseResult.Headings.H1),
			ImageCount:           len(parseResult.Images),
			LinkCount:            len(parseResult.Links),
		},
		Links:  links,
		Issues: detected,
	}

	return gomcp.NewToolResultJSON(result)
}

// redirectHopResult represents a single hop in the redirect chain response.
type redirectHopResult struct {
	HopIndex   int    `json:"hopIndex"`
	StatusCode int    `json:"statusCode"`
	FromURL    string `json:"fromUrl"`
	ToURL      string `json:"toUrl"`
}

// redirectResult is the JSON response from check_redirects.
type redirectResult struct {
	OriginalURL     string              `json:"originalUrl"`
	FinalURL        string              `json:"finalUrl"`
	FinalStatusCode int                 `json:"finalStatusCode"`
	TotalHops       int                 `json:"totalHops"`
	Hops            []redirectHopResult `json:"hops"`
	LoopDetected    bool                `json:"loopDetected"`
	HopsExceeded    bool                `json:"hopsExceeded"`
}

func (s *Server) handleCheckRedirects(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return gomcp.NewToolResultError("parameter \"url\" is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return gomcp.NewToolResultError(fmt.Sprintf("invalid URL %q: must be http or https", rawURL)), nil
	}

	if s.fetcher == nil {
		return gomcp.NewToolResultError("server not configured: fetcher unavailable"), nil
	}

	fetchResult, err := s.fetcher.Fetch(rawURL)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("fetching %q: %v", rawURL, err)), nil
	}
	if fetchResult.Error != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("fetching %q: %v", rawURL, fetchResult.Error)), nil
	}

	hops := make([]redirectHopResult, 0, len(fetchResult.RedirectHops))
	for _, h := range fetchResult.RedirectHops {
		hops = append(hops, redirectHopResult{
			HopIndex:   h.HopIndex,
			StatusCode: h.StatusCode,
			FromURL:    h.FromURL,
			ToURL:      h.ToURL,
		})
	}

	result := redirectResult{
		OriginalURL:     rawURL,
		FinalURL:        fetchResult.FinalURL,
		FinalStatusCode: fetchResult.StatusCode,
		TotalHops:       len(hops),
		Hops:            hops,
		LoopDetected:    fetchResult.RedirectLoopDetected,
		HopsExceeded:    fetchResult.RedirectHopsExceeded,
	}

	return gomcp.NewToolResultJSON(result)
}

// robotsTestResult holds the result of testing a path against robots.txt.
type robotsTestResult struct {
	Path    string `json:"path"`
	Allowed bool   `json:"allowed"`
}

// robotsResult is the JSON response from check_robots_txt.
type robotsResult struct {
	Host        string              `json:"host"`
	RobotsURL   string              `json:"robotsUrl"`
	RuleCount   int                 `json:"ruleCount"`
	Sitemaps    []string            `json:"sitemaps"`
	CrawlDelay  int                 `json:"crawlDelay"`
	TestResults []robotsTestResult  `json:"testResults,omitempty"`
	Directives  []robots.Directive  `json:"directives"`
}

func (s *Server) handleCheckRobotsTxt(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return gomcp.NewToolResultError("parameter \"url\" is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return gomcp.NewToolResultError(fmt.Sprintf("invalid URL %q: must be http or https", rawURL)), nil
	}

	userAgent := "*"
	if ua, ok := args["userAgent"].(string); ok && ua != "" {
		userAgent = ua
	}

	var testPaths []string
	if tp, ok := args["testPaths"].([]any); ok {
		for _, p := range tp {
			if ps, ok := p.(string); ok {
				testPaths = append(testPaths, ps)
			}
		}
	}

	// Build robots.txt URL.
	robotsURL := fmt.Sprintf("%s://%s/robots.txt", parsed.Scheme, parsed.Host)

	// Fetch robots.txt.
	var client *http.Client
	if s.fetcher != nil {
		client = s.fetcher.SafeClient()
	} else {
		client = &http.Client{}
	}

	resp, err := client.Get(robotsURL)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("fetching %q: %v", robotsURL, err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return gomcp.NewToolResultJSON(robotsResult{
			Host:       parsed.Host,
			RobotsURL:  robotsURL,
			RuleCount:  0,
			Sitemaps:   []string{},
			Directives: []robots.Directive{},
		})
	}

	bodyBytes := make([]byte, 0, 64*1024)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			bodyBytes = append(bodyBytes, buf[:n]...)
		}
		if readErr != nil {
			break
		}
		if len(bodyBytes) > 1024*1024 {
			break
		}
	}

	rf, err := robots.Parse(string(bodyBytes))
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("parsing robots.txt from %q: %v", robotsURL, err)), nil
	}

	result := robotsResult{
		Host:       parsed.Host,
		RobotsURL:  robotsURL,
		RuleCount:  len(rf.Rules),
		Sitemaps:   rf.Sitemaps,
		CrawlDelay: rf.CrawlDelay(userAgent),
		Directives: rf.Rules,
	}

	// Test paths if provided.
	if len(testPaths) > 0 {
		result.TestResults = make([]robotsTestResult, 0, len(testPaths))
		for _, path := range testPaths {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			result.TestResults = append(result.TestResults, robotsTestResult{
				Path:    path,
				Allowed: rf.IsAllowed(userAgent, path),
			})
		}
	}

	return gomcp.NewToolResultJSON(result)
}

// sitemapResult is the JSON response from parse_sitemap.
type sitemapResult struct {
	URL           string           `json:"url"`
	TotalEntries  int              `json:"totalEntries"`
	SitemapCount  int              `json:"sitemapCount"`
	Entries       []sitemap.Entry  `json:"entries"`
}

func (s *Server) handleParseSitemap(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return gomcp.NewToolResultError("parameter \"url\" is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return gomcp.NewToolResultError(fmt.Sprintf("invalid URL %q: must be http or https", rawURL)), nil
	}

	maxEntries := 10000
	if me, ok := args["maxEntries"].(float64); ok && me > 0 {
		maxEntries = int(me)
	}

	var client *http.Client
	if s.fetcher != nil {
		client = s.fetcher.SafeClient()
	} else {
		client = &http.Client{}
	}

	entries, sitemapCount, err := sitemap.FetchAndParse(rawURL, maxEntries, client)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("parsing sitemap %q: %v", rawURL, err)), nil
	}

	result := sitemapResult{
		URL:          rawURL,
		TotalEntries: len(entries),
		SitemapCount: sitemapCount,
		Entries:      entries,
	}

	return gomcp.NewToolResultJSON(result)
}

func marshalJSONLDBlocksForStandalone(blocks []parser.JSONLDBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return ""
	}
	return string(raw)
}
