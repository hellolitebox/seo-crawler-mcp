// Package config defines configuration types and defaults for the SEO crawler.
package config

import (
	"net/url"
	"path"
	"time"
)

// ScopeMode controls how crawl boundaries are determined.
type ScopeMode string

const (
	ScopeModeRegistrableDomain ScopeMode = "registrable_domain"
	ScopeModeExactHost         ScopeMode = "exact_host"
	ScopeModeAllowlist         ScopeMode = "allowlist"
)

// RenderMode controls when pages are rendered with a headless browser.
type RenderMode string

const (
	RenderModeStatic  RenderMode = "static"
	RenderModeHybrid  RenderMode = "hybrid"
	RenderModeBrowser RenderMode = "browser"
)

// RenderProvider controls where browser rendering runs.
type RenderProvider string

const (
	RenderProviderLocal       RenderProvider = "local"
	RenderProviderBrowserbase RenderProvider = "browserbase"
	RenderProviderAuto        RenderProvider = "auto"
)

// RobotsUnreachablePolicy controls behavior when robots.txt cannot be fetched.
type RobotsUnreachablePolicy string

const (
	RobotsUnreachablePolicyAllow          RobotsUnreachablePolicy = "allow"
	RobotsUnreachablePolicyDisallow       RobotsUnreachablePolicy = "disallow"
	RobotsUnreachablePolicyCacheThenAllow RobotsUnreachablePolicy = "cache_then_allow"
)

// URLGroupConfig defines overrides for specific URL patterns.
type URLGroupConfig struct {
	Name                 string `json:"name"`
	Pattern              string `json:"pattern"`
	ThinContentThreshold *int   `json:"thinContentThreshold,omitempty"`
}

// Config holds all configuration for the SEO crawler MCP server.
type Config struct {
	// Crawl scope
	ScopeMode    ScopeMode `json:"scopeMode"`
	AllowedHosts []string  `json:"allowedHosts,omitempty"`

	// HTTP client
	RequestTimeout      time.Duration `json:"requestTimeout"`
	MaxResponseBody     int64         `json:"maxResponseBody"`
	MaxDecompressedBody int64         `json:"maxDecompressedBody"`
	UserAgent           string        `json:"userAgent"`
	Retries             int           `json:"retries"`
	MaxRedirectHops     int           `json:"maxRedirectHops"`

	// Concurrency
	PerHostConcurrency int `json:"perHostConcurrency"`
	GlobalConcurrency  int `json:"globalConcurrency"`

	// Crawl limits
	MaxPages          int           `json:"maxPages"`
	MaxDepth          int           `json:"maxDepth"`
	MaxDiscoveredURLs int           `json:"maxDiscoveredUrls"`
	MaxOnboardedHosts int           `json:"maxOnboardedHosts"`
	MaxCrawlDuration  time.Duration `json:"maxCrawlDuration"`

	// Rendering
	RenderMode           RenderMode     `json:"renderMode"`
	RenderProvider       RenderProvider `json:"renderProvider"`
	RenderWaitMs         int            `json:"renderWaitMs"`
	MaxBrowserInstances  int            `json:"maxBrowserInstances"`
	BrowserRenderTimeout time.Duration  `json:"browserRenderTimeout"`
	BrowserbaseAPIKey    string         `json:"browserbaseApiKey,omitempty"`
	BrowserbaseProjectID string         `json:"browserbaseProjectId,omitempty"`
	ForceRenderPatterns  []string       `json:"forceRenderPatterns,omitempty"`

	// Robots
	RespectRobots           bool                    `json:"respectRobots"`
	RobotsUnreachablePolicy RobotsUnreachablePolicy `json:"robotsUnreachablePolicy"`

	// URL normalization
	IgnoreParams            []string `json:"ignoreParams"`
	MaxQueryVariantsPerPath int      `json:"maxQueryVariantsPerPath"`

	// Security
	AllowInsecureTLS     bool `json:"allowInsecureTLS"`
	AllowPrivateNetworks bool `json:"allowPrivateNetworks"`
	SSRFProtection       bool `json:"ssrfProtection"`

	// SEO thresholds
	TitleMaxLength       int `json:"titleMaxLength"`
	TitleMinLength       int `json:"titleMinLength"`
	DescriptionMaxLength int `json:"descriptionMaxLength"`
	DescriptionMinLength int `json:"descriptionMinLength"`
	ThinContentThreshold int `json:"thinContentThreshold"`
	DeepPageThreshold    int `json:"deepPageThreshold"`

	// Rate limiting
	MaxConcurrentCrawls  int           `json:"maxConcurrentCrawls"`
	MaxConcurrentAnalyze int           `json:"maxConcurrentAnalyze"`
	MaxJobsPerHour       int           `json:"maxJobsPerHour"`
	AnalyzeJobTTL        time.Duration `json:"analyzeJobTTL"`

	// Sitemap
	MaxSitemapEntries int `json:"maxSitemapEntries"`

	// Resources
	MaxQueueMemoryMB int    `json:"maxQueueMemoryMB"`
	DBPath           string `json:"dbPath"`
	// MaxJobAge is the maximum age of completed jobs before purge. 0 means disabled.
	MaxJobAge time.Duration `json:"maxJobAge"`

	// PageSpeed Insights API key (empty = skip PSI audits)
	PSIAPIKey string `json:"psiApiKey"`

	// PSIMaxPages limits PageSpeed Insights audits to the first N eligible pages.
	// 0 means audit every eligible page.
	PSIMaxPages int `json:"psiMaxPages"`

	// PSIDesktop enables desktop strategy in addition to mobile (default: mobile only).
	PSIDesktop bool `json:"psiDesktop"`

	// AxeMaxPages limits axe accessibility audits to the first N pages.
	// 0 means audit every page.
	AxeMaxPages int `json:"axeMaxPages"`

	// GrammarMaxPages limits LanguageTool grammar/spelling checks to the first N pages.
	// 0 means check every eligible page.
	GrammarMaxPages int `json:"grammarMaxPages"`

	// LanguageToolURL is the base URL of a LanguageTool server for text quality checks.
	// If empty, text quality checks are skipped.
	LanguageToolURL string `json:"languageToolUrl"`

	// URL group overrides
	URLGroups []URLGroupConfig `json:"urlGroups"`
}

// MatchesForceRender returns true if the URL's path matches any ForceRenderPatterns.
// Patterns use path.Match syntax (e.g., "/app/*").
func (c *Config) MatchesForceRender(rawURL string) bool {
	if len(c.ForceRenderPatterns) == 0 {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	p := parsed.Path
	for _, pattern := range c.ForceRenderPatterns {
		if matched, _ := path.Match(pattern, p); matched {
			return true
		}
	}
	return false
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		ScopeMode:    ScopeModeRegistrableDomain,
		AllowedHosts: []string{},

		RequestTimeout:      10 * time.Second,
		MaxResponseBody:     5 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		UserAgent:           "seo-crawler-mcp/0.1",
		Retries:             1,
		MaxRedirectHops:     10,

		PerHostConcurrency: 2,
		GlobalConcurrency:  8,

		MaxPages:          10000,
		MaxDepth:          50,
		MaxDiscoveredURLs: 100000,
		MaxOnboardedHosts: 50,
		MaxCrawlDuration:  30 * time.Minute,

		RenderMode:           RenderModeHybrid,
		RenderProvider:       RenderProviderAuto,
		RenderWaitMs:         2000,
		MaxBrowserInstances:  2,
		BrowserRenderTimeout: 30 * time.Second,
		ForceRenderPatterns:  []string{},

		RespectRobots:           true,
		RobotsUnreachablePolicy: RobotsUnreachablePolicyAllow,

		IgnoreParams: []string{
			"utm_*",
			"fbclid",
			"gclid",
			"gbraid",
			"wbraid",
			"msclkid",
			"mc_cid",
			"mc_eid",
		},
		MaxQueryVariantsPerPath: 50,

		AllowInsecureTLS:     false,
		AllowPrivateNetworks: false,
		SSRFProtection:       true,

		TitleMaxLength:       60,
		TitleMinLength:       30,
		DescriptionMaxLength: 160,
		DescriptionMinLength: 70,
		ThinContentThreshold: 200,
		DeepPageThreshold:    3,

		MaxConcurrentCrawls:  3,
		MaxConcurrentAnalyze: 50,
		MaxJobsPerHour:       20,
		AnalyzeJobTTL:        24 * time.Hour,

		MaxSitemapEntries: 500000,

		MaxQueueMemoryMB: 100,
		DBPath:           "seo-crawler.db",
		MaxJobAge:        0,
		PSIMaxPages:      50,
		AxeMaxPages:      50,
		GrammarMaxPages:  50,

		URLGroups: []URLGroupConfig{},
	}
}
