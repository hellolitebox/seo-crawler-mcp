package config

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Scope
	if cfg.ScopeMode != ScopeModeRegistrableDomain {
		t.Errorf("ScopeMode = %q, want %q", cfg.ScopeMode, ScopeModeRegistrableDomain)
	}
	if cfg.AllowedHosts == nil {
		t.Error("AllowedHosts should be initialized, not nil")
	}
	if len(cfg.AllowedHosts) != 0 {
		t.Errorf("AllowedHosts has %d items, want 0", len(cfg.AllowedHosts))
	}

	// HTTP client
	if cfg.RequestTimeout != 10*time.Second {
		t.Errorf("RequestTimeout = %v, want 10s", cfg.RequestTimeout)
	}
	if cfg.MaxResponseBody != 5*1024*1024 {
		t.Errorf("MaxResponseBody = %d, want 5MB", cfg.MaxResponseBody)
	}
	if cfg.MaxDecompressedBody != 20*1024*1024 {
		t.Errorf("MaxDecompressedBody = %d, want 20MB", cfg.MaxDecompressedBody)
	}
	if cfg.UserAgent != "seo-crawler-mcp/0.1" {
		t.Errorf("UserAgent = %q, want %q", cfg.UserAgent, "seo-crawler-mcp/0.1")
	}
	if cfg.Retries != 1 {
		t.Errorf("Retries = %d, want 1", cfg.Retries)
	}
	if cfg.MaxRedirectHops != 10 {
		t.Errorf("MaxRedirectHops = %d, want 10", cfg.MaxRedirectHops)
	}

	// Concurrency
	if cfg.PerHostConcurrency != 2 {
		t.Errorf("PerHostConcurrency = %d, want 2", cfg.PerHostConcurrency)
	}
	if cfg.GlobalConcurrency != 8 {
		t.Errorf("GlobalConcurrency = %d, want 8", cfg.GlobalConcurrency)
	}

	// Crawl limits
	if cfg.MaxPages != 10000 {
		t.Errorf("MaxPages = %d, want 10000", cfg.MaxPages)
	}
	if cfg.MaxDepth != 50 {
		t.Errorf("MaxDepth = %d, want 50", cfg.MaxDepth)
	}
	if cfg.MaxDiscoveredURLs != 100000 {
		t.Errorf("MaxDiscoveredURLs = %d, want 100000", cfg.MaxDiscoveredURLs)
	}
	if cfg.MaxOnboardedHosts != 50 {
		t.Errorf("MaxOnboardedHosts = %d, want 50", cfg.MaxOnboardedHosts)
	}
	if cfg.MaxCrawlDuration != 30*time.Minute {
		t.Errorf("MaxCrawlDuration = %v, want 30m", cfg.MaxCrawlDuration)
	}

	// Rendering
	if cfg.RenderMode != RenderModeHybrid {
		t.Errorf("RenderMode = %q, want %q", cfg.RenderMode, RenderModeHybrid)
	}
	if cfg.RenderWaitMs != 2000 {
		t.Errorf("RenderWaitMs = %d, want 2000", cfg.RenderWaitMs)
	}
	if cfg.MaxBrowserInstances != 2 {
		t.Errorf("MaxBrowserInstances = %d, want 2", cfg.MaxBrowserInstances)
	}
	if cfg.BrowserRenderTimeout != 30*time.Second {
		t.Errorf("BrowserRenderTimeout = %v, want 30s", cfg.BrowserRenderTimeout)
	}
	if cfg.ForceRenderPatterns == nil {
		t.Error("ForceRenderPatterns should be initialized, not nil")
	}
	if len(cfg.ForceRenderPatterns) != 0 {
		t.Errorf("ForceRenderPatterns has %d items, want 0", len(cfg.ForceRenderPatterns))
	}

	// Robots
	if !cfg.RespectRobots {
		t.Error("RespectRobots should be true")
	}
	if cfg.RobotsUnreachablePolicy != RobotsUnreachablePolicyAllow {
		t.Errorf(
			"RobotsUnreachablePolicy = %q, want %q",
			cfg.RobotsUnreachablePolicy,
			RobotsUnreachablePolicyAllow,
		)
	}

	// URL normalization
	if len(cfg.IgnoreParams) != 8 {
		t.Errorf("IgnoreParams has %d items, want 8", len(cfg.IgnoreParams))
	}
	if cfg.MaxQueryVariantsPerPath != 50 {
		t.Errorf("MaxQueryVariantsPerPath = %d, want 50", cfg.MaxQueryVariantsPerPath)
	}
	if cfg.PSIMaxPages != 50 {
		t.Errorf("PSIMaxPages = %d, want 50", cfg.PSIMaxPages)
	}
	if cfg.AxeMaxPages != 50 {
		t.Errorf("AxeMaxPages = %d, want 50", cfg.AxeMaxPages)
	}
	if cfg.GrammarMaxPages != 50 {
		t.Errorf("GrammarMaxPages = %d, want 50", cfg.GrammarMaxPages)
	}

	// Security
	if cfg.AllowInsecureTLS {
		t.Error("AllowInsecureTLS should be false")
	}
	if cfg.AllowPrivateNetworks {
		t.Error("AllowPrivateNetworks should be false")
	}
	if !cfg.SSRFProtection {
		t.Error("SSRFProtection should be true")
	}

	// SEO thresholds
	if cfg.TitleMaxLength != 60 {
		t.Errorf("TitleMaxLength = %d, want 60", cfg.TitleMaxLength)
	}
	if cfg.TitleMinLength != 30 {
		t.Errorf("TitleMinLength = %d, want 30", cfg.TitleMinLength)
	}
	if cfg.DescriptionMaxLength != 160 {
		t.Errorf("DescriptionMaxLength = %d, want 160", cfg.DescriptionMaxLength)
	}
	if cfg.DescriptionMinLength != 70 {
		t.Errorf("DescriptionMinLength = %d, want 70", cfg.DescriptionMinLength)
	}
	if cfg.ThinContentThreshold != 200 {
		t.Errorf("ThinContentThreshold = %d, want 200", cfg.ThinContentThreshold)
	}
	if cfg.DeepPageThreshold != 3 {
		t.Errorf("DeepPageThreshold = %d, want 3", cfg.DeepPageThreshold)
	}

	// Rate limiting
	if cfg.MaxConcurrentCrawls != 3 {
		t.Errorf("MaxConcurrentCrawls = %d, want 3", cfg.MaxConcurrentCrawls)
	}
	if cfg.MaxConcurrentAnalyze != 50 {
		t.Errorf("MaxConcurrentAnalyze = %d, want 50", cfg.MaxConcurrentAnalyze)
	}
	if cfg.MaxJobsPerHour != 20 {
		t.Errorf("MaxJobsPerHour = %d, want 20", cfg.MaxJobsPerHour)
	}
	if cfg.AnalyzeJobTTL != 24*time.Hour {
		t.Errorf("AnalyzeJobTTL = %v, want 24h", cfg.AnalyzeJobTTL)
	}

	// Sitemap
	if cfg.MaxSitemapEntries != 500000 {
		t.Errorf("MaxSitemapEntries = %d, want 500000", cfg.MaxSitemapEntries)
	}

	// Resources
	if cfg.MaxQueueMemoryMB != 100 {
		t.Errorf("MaxQueueMemoryMB = %d, want 100", cfg.MaxQueueMemoryMB)
	}
	if cfg.DBPath != "seo-crawler.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "seo-crawler.db")
	}
	if cfg.MaxJobAge != 0 {
		t.Errorf("MaxJobAge = %v, want 0 (disabled)", cfg.MaxJobAge)
	}

	// URL groups initialized but empty
	if cfg.URLGroups == nil {
		t.Error("URLGroups should be initialized, not nil")
	}
	if len(cfg.URLGroups) != 0 {
		t.Errorf("URLGroups has %d items, want 0", len(cfg.URLGroups))
	}
}

func TestScopeModeConstants(t *testing.T) {
	if string(ScopeModeRegistrableDomain) != "registrable_domain" {
		t.Errorf("ScopeModeRegistrableDomain = %q", ScopeModeRegistrableDomain)
	}
	if string(ScopeModeExactHost) != "exact_host" {
		t.Errorf("ScopeModeExactHost = %q", ScopeModeExactHost)
	}
	if string(ScopeModeAllowlist) != "allowlist" {
		t.Errorf("ScopeModeAllowlist = %q", ScopeModeAllowlist)
	}
}

func TestRenderModeConstants(t *testing.T) {
	if string(RenderModeStatic) != "static" {
		t.Errorf("RenderModeStatic = %q", RenderModeStatic)
	}
	if string(RenderModeHybrid) != "hybrid" {
		t.Errorf("RenderModeHybrid = %q", RenderModeHybrid)
	}
	if string(RenderModeBrowser) != "browser" {
		t.Errorf("RenderModeBrowser = %q", RenderModeBrowser)
	}
}

func TestRobotsUnreachablePolicyConstants(t *testing.T) {
	if string(RobotsUnreachablePolicyAllow) != "allow" {
		t.Errorf("RobotsUnreachablePolicyAllow = %q", RobotsUnreachablePolicyAllow)
	}
	if string(RobotsUnreachablePolicyDisallow) != "disallow" {
		t.Errorf("RobotsUnreachablePolicyDisallow = %q", RobotsUnreachablePolicyDisallow)
	}
	if string(RobotsUnreachablePolicyCacheThenAllow) != "cache_then_allow" {
		t.Errorf("RobotsUnreachablePolicyCacheThenAllow = %q", RobotsUnreachablePolicyCacheThenAllow)
	}
}
