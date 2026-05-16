package mcp

import (
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// Tool definitions for all 10 MCP tools.
var (
	crawlSiteTool = gomcp.NewTool("crawl_site",
		gomcp.WithDescription("Start a new site crawl. Returns a job ID for tracking progress."),
		gomcp.WithReadOnlyHintAnnotation(false),
		gomcp.WithOpenWorldHintAnnotation(true),
		gomcp.WithString("url", gomcp.Required(), gomcp.Description("Seed domain or http(s) URL to begin crawling; domains without a scheme default to https")),
		gomcp.WithArray("urls", gomcp.Description("Additional seed domains or http(s) URLs; domains without a scheme default to https"), gomcp.WithStringItems()),
		gomcp.WithString("scopeMode", gomcp.Description("Crawl scope boundary"), gomcp.Enum("registrable_domain", "exact_host", "allowlist")),
		gomcp.WithArray("allowedHosts", gomcp.Description("Hosts to allow when scopeMode is allowlist"), gomcp.WithStringItems()),
		gomcp.WithNumber("maxPages", gomcp.Description("Maximum pages to crawl (default 10000)")),
		gomcp.WithNumber("maxDepth", gomcp.Description("Maximum link depth (default 50)")),
		gomcp.WithString("renderMode", gomcp.Description("Page rendering strategy"), gomcp.Enum("static", "browser", "hybrid")),
		gomcp.WithBoolean("respectRobots", gomcp.Description("Honor robots.txt directives (default true)")),
		gomcp.WithBoolean("dryRun", gomcp.Description("Fetch seeds only without full crawl (default false)")),
	)

	crawlStatusTool = gomcp.NewTool("crawl_status",
		gomcp.WithDescription("Get the current status and progress of a crawl job."),
		gomcp.WithReadOnlyHintAnnotation(true),
		gomcp.WithOpenWorldHintAnnotation(false),
		gomcp.WithString("jobId", gomcp.Required(), gomcp.Description("Crawl job ID")),
	)

	cancelCrawlTool = gomcp.NewTool("cancel_crawl",
		gomcp.WithDescription("Cancel a running crawl job."),
		gomcp.WithReadOnlyHintAnnotation(false),
		gomcp.WithOpenWorldHintAnnotation(false),
		gomcp.WithString("jobId", gomcp.Required(), gomcp.Description("Crawl job ID to cancel")),
	)

	getCrawlSummaryTool = gomcp.NewTool("get_crawl_summary",
		gomcp.WithDescription("Get a high-level summary of crawl results including issue counts and page statistics."),
		gomcp.WithReadOnlyHintAnnotation(true),
		gomcp.WithOpenWorldHintAnnotation(false),
		gomcp.WithString("jobId", gomcp.Required(), gomcp.Description("Crawl job ID")),
	)

	getCrawlResultsTool = gomcp.NewTool("get_crawl_results",
		gomcp.WithDescription("Query detailed crawl results with filtering and pagination."),
		gomcp.WithReadOnlyHintAnnotation(true),
		gomcp.WithOpenWorldHintAnnotation(false),
		gomcp.WithString("jobId", gomcp.Required(), gomcp.Description("Crawl job ID")),
		gomcp.WithString("view", gomcp.Description("Result view: pages, issues, external_links, response_codes"), gomcp.Enum("pages", "issues", "external_links", "response_codes")),
		gomcp.WithNumber("limit", gomcp.Description("Maximum results to return (default 50, max 500)")),
		gomcp.WithString("cursor", gomcp.Description("Pagination cursor (base64)")),
		gomcp.WithString("issueType", gomcp.Description("Filter by issue type")),
		gomcp.WithString("statusCodeFamily", gomcp.Description("Filter by status code family: 2xx, 3xx, 4xx, 5xx")),
		gomcp.WithString("urlPattern", gomcp.Description("Filter by URL pattern (substring match)")),
		gomcp.WithString("urlGroup", gomcp.Description("Filter by URL group")),
		gomcp.WithNumber("minDepth", gomcp.Description("Filter by minimum depth")),
		gomcp.WithNumber("maxDepth", gomcp.Description("Filter by maximum depth")),
		gomcp.WithString("relationType", gomcp.Description("Filter by edge relation type")),
		gomcp.WithString("contentType", gomcp.Description("Filter by content type")),
		gomcp.WithString("targetDomain", gomcp.Description("Filter by target domain")),
	)

	getLinkGraphTool = gomcp.NewTool("get_link_graph",
		gomcp.WithDescription("Get the link graph (edges) for a crawl job, centered on a specific URL."),
		gomcp.WithReadOnlyHintAnnotation(true),
		gomcp.WithOpenWorldHintAnnotation(false),
		gomcp.WithString("jobId", gomcp.Required(), gomcp.Description("Crawl job ID")),
		gomcp.WithNumber("urlId", gomcp.Required(), gomcp.Description("URL ID to query edges for")),
		gomcp.WithString("direction", gomcp.Description("Edge direction: inbound, outbound, both"), gomcp.Enum("outbound", "inbound", "both")),
		gomcp.WithNumber("limit", gomcp.Description("Maximum edges to return (default 50, max 500)")),
		gomcp.WithString("cursor", gomcp.Description("Pagination cursor")),
		gomcp.WithString("relationType", gomcp.Description("Filter by relation type")),
		gomcp.WithString("sourceKind", gomcp.Description("Filter by source kind")),
	)

	analyzeURLTool = gomcp.NewTool("analyze_url",
		gomcp.WithDescription("Analyze a single URL for SEO issues without a full crawl."),
		gomcp.WithReadOnlyHintAnnotation(false),
		gomcp.WithOpenWorldHintAnnotation(true),
		gomcp.WithString("url", gomcp.Required(), gomcp.Description("Domain or http(s) URL to analyze; domains without a scheme default to https")),
		gomcp.WithString("renderMode", gomcp.Description("Rendering strategy"), gomcp.Enum("static", "browser")),
	)

	checkRedirectsTool = gomcp.NewTool("check_redirects",
		gomcp.WithDescription("Follow and report the redirect chain for a URL."),
		gomcp.WithReadOnlyHintAnnotation(true),
		gomcp.WithOpenWorldHintAnnotation(true),
		gomcp.WithString("url", gomcp.Required(), gomcp.Description("Domain or http(s) URL to check redirects for; domains without a scheme default to https")),
		gomcp.WithNumber("maxHops", gomcp.Description("Maximum redirect hops to follow (default 10)")),
	)

	checkRobotsTxtTool = gomcp.NewTool("check_robots_txt",
		gomcp.WithDescription("Fetch and parse robots.txt for a given host."),
		gomcp.WithReadOnlyHintAnnotation(true),
		gomcp.WithOpenWorldHintAnnotation(true),
		gomcp.WithString("url", gomcp.Required(), gomcp.Description("Domain or http(s) URL whose host's robots.txt to check; domains without a scheme default to https")),
		gomcp.WithString("userAgent", gomcp.Description("User-agent to test rules against")),
		gomcp.WithArray("testPaths", gomcp.Description("Paths to test against robots.txt rules"), gomcp.WithStringItems()),
	)

	parseSitemapTool = gomcp.NewTool("parse_sitemap",
		gomcp.WithDescription("Parse a sitemap XML and return its entries."),
		gomcp.WithReadOnlyHintAnnotation(true),
		gomcp.WithOpenWorldHintAnnotation(true),
		gomcp.WithString("url", gomcp.Required(), gomcp.Description("Sitemap domain/path or http(s) URL to parse; URLs without a scheme default to https")),
		gomcp.WithNumber("maxEntries", gomcp.Description("Maximum entries to return (default 10000)")),
	)
)

// registerTools adds all tool handlers to the MCP server.
func (s *Server) registerTools() {
	// Crawl lifecycle
	s.mcpServer.AddTool(crawlSiteTool, s.handleCrawlSite)
	s.mcpServer.AddTool(crawlStatusTool, s.handleCrawlStatus)
	s.mcpServer.AddTool(cancelCrawlTool, s.handleCancelCrawl)

	// Query tools
	s.mcpServer.AddTool(getCrawlSummaryTool, s.handleGetCrawlSummary)
	s.mcpServer.AddTool(getCrawlResultsTool, s.handleGetCrawlResults)
	s.mcpServer.AddTool(getLinkGraphTool, s.handleGetLinkGraph)

	// Standalone tools
	s.mcpServer.AddTool(analyzeURLTool, s.handleAnalyzeURL)
	s.mcpServer.AddTool(checkRedirectsTool, s.handleCheckRedirects)
	s.mcpServer.AddTool(checkRobotsTxtTool, s.handleCheckRobotsTxt)
	s.mcpServer.AddTool(parseSitemapTool, s.handleParseSitemap)
}
