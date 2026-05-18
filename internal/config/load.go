package config

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// tomlConfig mirrors Config for TOML decoding with string-based durations.
type tomlConfig struct {
	ScopeMode    string   `toml:"scope_mode"`
	AllowedHosts []string `toml:"allowed_hosts"`

	RequestTimeout      string `toml:"request_timeout"`
	MaxResponseBody     int64  `toml:"max_response_body"`
	MaxDecompressedBody int64  `toml:"max_decompressed_body"`
	UserAgent           string `toml:"user_agent"`
	Retries             int    `toml:"retries"`
	MaxRedirectHops     int    `toml:"max_redirect_hops"`

	PerHostConcurrency int `toml:"per_host_concurrency"`
	GlobalConcurrency  int `toml:"global_concurrency"`

	MaxPages          int    `toml:"max_pages"`
	MaxDepth          int    `toml:"max_depth"`
	MaxDiscoveredURLs int    `toml:"max_discovered_urls"`
	MaxOnboardedHosts int    `toml:"max_onboarded_hosts"`
	MaxCrawlDuration  string `toml:"max_crawl_duration"`

	RenderMode           string   `toml:"render_mode"`
	RenderProvider       string   `toml:"render_provider"`
	RenderWaitMs         int      `toml:"render_wait_ms"`
	MaxBrowserInstances  int      `toml:"max_browser_instances"`
	BrowserRenderTimeout string   `toml:"browser_render_timeout"`
	BrowserbaseAPIKey    string   `toml:"browserbase_api_key"`
	BrowserbaseProjectID string   `toml:"browserbase_project_id"`
	ForceRenderPatterns  []string `toml:"force_render_patterns"`

	RespectRobots           *bool  `toml:"respect_robots"`
	RobotsUnreachablePolicy string `toml:"robots_unreachable_policy"`

	IgnoreParams            []string `toml:"ignore_params"`
	MaxQueryVariantsPerPath int      `toml:"max_query_variants_per_path"`

	AllowInsecureTLS     *bool `toml:"allow_insecure_tls"`
	AllowPrivateNetworks *bool `toml:"allow_private_networks"`
	SSRFProtection       *bool `toml:"ssrf_protection"`

	TitleMaxLength       int `toml:"title_max_length"`
	TitleMinLength       int `toml:"title_min_length"`
	DescriptionMaxLength int `toml:"description_max_length"`
	DescriptionMinLength int `toml:"description_min_length"`
	ThinContentThreshold int `toml:"thin_content_threshold"`
	DeepPageThreshold    int `toml:"deep_page_threshold"`

	MaxConcurrentCrawls  int    `toml:"max_concurrent_crawls"`
	MaxConcurrentAnalyze int    `toml:"max_concurrent_analyze"`
	MaxJobsPerHour       int    `toml:"max_jobs_per_hour"`
	AnalyzeJobTTL        string `toml:"analyze_job_ttl"`

	MaxSitemapEntries int `toml:"max_sitemap_entries"`

	MaxQueueMemoryMB int              `toml:"max_queue_memory_mb"`
	DBPath           string           `toml:"db_path"`
	MaxJobAge        string           `toml:"max_job_age"`
	PSIAPIKey        string           `toml:"psi_api_key"`
	PSIMaxPages      *int             `toml:"psi_max_pages"`
	PSIDesktop       bool             `toml:"psi_desktop"`
	AxeMaxPages      *int             `toml:"axe_max_pages"`
	GrammarMaxPages  *int             `toml:"grammar_max_pages"`
	LanguageToolURL  string           `toml:"languagetool_url"`
	URLGroups        []URLGroupConfig `toml:"url_groups"`
}

// LoadConfig loads configuration with precedence: env vars > config file > defaults.
func LoadConfig(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath != "" {
		fileCfg, err := LoadFromFile(configPath)
		if err != nil {
			return nil, err
		}
		cfg = *fileCfg
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return nil, err
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadFromFile loads config from a TOML file, merging with defaults.
func LoadFromFile(path string) (*Config, error) {
	cfg := DefaultConfig()

	var tc tomlConfig
	if _, err := toml.DecodeFile(path, &tc); err != nil {
		return nil, fmt.Errorf("loading config file %q: %w", path, err)
	}

	// Apply non-zero TOML values over defaults.
	if tc.ScopeMode != "" {
		cfg.ScopeMode = ScopeMode(tc.ScopeMode)
	}
	if tc.AllowedHosts != nil {
		cfg.AllowedHosts = tc.AllowedHosts
	}
	if tc.RequestTimeout != "" {
		d, err := time.ParseDuration(tc.RequestTimeout)
		if err != nil {
			return nil, fmt.Errorf("parsing request_timeout %q: %w", tc.RequestTimeout, err)
		}
		cfg.RequestTimeout = d
	}
	if tc.MaxResponseBody != 0 {
		cfg.MaxResponseBody = tc.MaxResponseBody
	}
	if tc.MaxDecompressedBody != 0 {
		cfg.MaxDecompressedBody = tc.MaxDecompressedBody
	}
	if tc.UserAgent != "" {
		cfg.UserAgent = tc.UserAgent
	}
	if tc.Retries != 0 {
		cfg.Retries = tc.Retries
	}
	if tc.MaxRedirectHops != 0 {
		cfg.MaxRedirectHops = tc.MaxRedirectHops
	}
	if tc.PerHostConcurrency != 0 {
		cfg.PerHostConcurrency = tc.PerHostConcurrency
	}
	if tc.GlobalConcurrency != 0 {
		cfg.GlobalConcurrency = tc.GlobalConcurrency
	}
	if tc.MaxPages != 0 {
		cfg.MaxPages = tc.MaxPages
	}
	if tc.MaxDepth != 0 {
		cfg.MaxDepth = tc.MaxDepth
	}
	if tc.MaxDiscoveredURLs != 0 {
		cfg.MaxDiscoveredURLs = tc.MaxDiscoveredURLs
	}
	if tc.MaxOnboardedHosts != 0 {
		cfg.MaxOnboardedHosts = tc.MaxOnboardedHosts
	}
	if tc.MaxCrawlDuration != "" {
		d, err := time.ParseDuration(tc.MaxCrawlDuration)
		if err != nil {
			return nil, fmt.Errorf("parsing max_crawl_duration %q: %w", tc.MaxCrawlDuration, err)
		}
		cfg.MaxCrawlDuration = d
	}
	if tc.RenderMode != "" {
		cfg.RenderMode = RenderMode(tc.RenderMode)
	}
	if tc.RenderProvider != "" {
		cfg.RenderProvider = RenderProvider(tc.RenderProvider)
	}
	if tc.RenderWaitMs != 0 {
		cfg.RenderWaitMs = tc.RenderWaitMs
	}
	if tc.MaxBrowserInstances != 0 {
		cfg.MaxBrowserInstances = tc.MaxBrowserInstances
	}
	if tc.BrowserRenderTimeout != "" {
		d, err := time.ParseDuration(tc.BrowserRenderTimeout)
		if err != nil {
			return nil, fmt.Errorf("parsing browser_render_timeout %q: %w", tc.BrowserRenderTimeout, err)
		}
		cfg.BrowserRenderTimeout = d
	}
	if tc.BrowserbaseAPIKey != "" {
		cfg.BrowserbaseAPIKey = tc.BrowserbaseAPIKey
	}
	if tc.BrowserbaseProjectID != "" {
		cfg.BrowserbaseProjectID = tc.BrowserbaseProjectID
	}
	if tc.ForceRenderPatterns != nil {
		cfg.ForceRenderPatterns = tc.ForceRenderPatterns
	}
	if tc.RespectRobots != nil {
		cfg.RespectRobots = *tc.RespectRobots
	}
	if tc.RobotsUnreachablePolicy != "" {
		cfg.RobotsUnreachablePolicy = RobotsUnreachablePolicy(tc.RobotsUnreachablePolicy)
	}
	if tc.IgnoreParams != nil {
		cfg.IgnoreParams = tc.IgnoreParams
	}
	if tc.MaxQueryVariantsPerPath != 0 {
		cfg.MaxQueryVariantsPerPath = tc.MaxQueryVariantsPerPath
	}
	if tc.AllowInsecureTLS != nil {
		cfg.AllowInsecureTLS = *tc.AllowInsecureTLS
	}
	if tc.AllowPrivateNetworks != nil {
		cfg.AllowPrivateNetworks = *tc.AllowPrivateNetworks
	}
	if tc.SSRFProtection != nil {
		cfg.SSRFProtection = *tc.SSRFProtection
	}
	if tc.TitleMaxLength != 0 {
		cfg.TitleMaxLength = tc.TitleMaxLength
	}
	if tc.TitleMinLength != 0 {
		cfg.TitleMinLength = tc.TitleMinLength
	}
	if tc.DescriptionMaxLength != 0 {
		cfg.DescriptionMaxLength = tc.DescriptionMaxLength
	}
	if tc.DescriptionMinLength != 0 {
		cfg.DescriptionMinLength = tc.DescriptionMinLength
	}
	if tc.ThinContentThreshold != 0 {
		cfg.ThinContentThreshold = tc.ThinContentThreshold
	}
	if tc.DeepPageThreshold != 0 {
		cfg.DeepPageThreshold = tc.DeepPageThreshold
	}
	if tc.MaxConcurrentCrawls != 0 {
		cfg.MaxConcurrentCrawls = tc.MaxConcurrentCrawls
	}
	if tc.MaxConcurrentAnalyze != 0 {
		cfg.MaxConcurrentAnalyze = tc.MaxConcurrentAnalyze
	}
	if tc.MaxJobsPerHour != 0 {
		cfg.MaxJobsPerHour = tc.MaxJobsPerHour
	}
	if tc.AnalyzeJobTTL != "" {
		d, err := time.ParseDuration(tc.AnalyzeJobTTL)
		if err != nil {
			return nil, fmt.Errorf("parsing analyze_job_ttl %q: %w", tc.AnalyzeJobTTL, err)
		}
		cfg.AnalyzeJobTTL = d
	}
	if tc.MaxSitemapEntries != 0 {
		cfg.MaxSitemapEntries = tc.MaxSitemapEntries
	}
	if tc.MaxQueueMemoryMB != 0 {
		cfg.MaxQueueMemoryMB = tc.MaxQueueMemoryMB
	}
	if tc.DBPath != "" {
		cfg.DBPath = tc.DBPath
	}
	if tc.MaxJobAge != "" {
		d, err := time.ParseDuration(tc.MaxJobAge)
		if err != nil {
			return nil, fmt.Errorf("parsing max_job_age %q: %w", tc.MaxJobAge, err)
		}
		cfg.MaxJobAge = d
	}
	if tc.PSIAPIKey != "" {
		cfg.PSIAPIKey = tc.PSIAPIKey
	}
	if tc.PSIMaxPages != nil {
		cfg.PSIMaxPages = *tc.PSIMaxPages
	}
	if tc.PSIDesktop {
		cfg.PSIDesktop = true
	}
	if tc.AxeMaxPages != nil {
		cfg.AxeMaxPages = *tc.AxeMaxPages
	}
	if tc.GrammarMaxPages != nil {
		cfg.GrammarMaxPages = *tc.GrammarMaxPages
	}
	if tc.LanguageToolURL != "" {
		cfg.LanguageToolURL = tc.LanguageToolURL
	}
	if tc.URLGroups != nil {
		cfg.URLGroups = tc.URLGroups
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyEnvOverrides reads SEO_CRAWLER_ prefixed env vars and overrides config values.
func applyEnvOverrides(cfg *Config) error {
	if v := os.Getenv("SEO_CRAWLER_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxPages = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_USER_AGENT"); v != "" {
		cfg.UserAgent = v
	}
	if v := os.Getenv("SEO_CRAWLER_SCOPE_MODE"); v != "" {
		cfg.ScopeMode = ScopeMode(v)
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxDepth = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_DISCOVERED_URLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxDiscoveredURLs = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_ONBOARDED_HOSTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxOnboardedHosts = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_CRAWL_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.MaxCrawlDuration = d
		}
	}
	if v := os.Getenv("SEO_CRAWLER_REQUEST_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.RequestTimeout = d
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_RESPONSE_BODY"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MaxResponseBody = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_DECOMPRESSED_BODY"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MaxDecompressedBody = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Retries = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_REDIRECT_HOPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxRedirectHops = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_PER_HOST_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PerHostConcurrency = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_GLOBAL_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.GlobalConcurrency = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_RENDER_MODE"); v != "" {
		cfg.RenderMode = RenderMode(v)
	}
	if v := os.Getenv("SEO_CRAWLER_RENDER_PROVIDER"); v != "" {
		cfg.RenderProvider = RenderProvider(v)
	}
	if v := os.Getenv("SEO_CRAWLER_RENDER_WAIT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RenderWaitMs = n
		}
	}
	if v := os.Getenv("BROWSERBASE_API_KEY"); v != "" {
		cfg.BrowserbaseAPIKey = v
	}
	if v := os.Getenv("BROWSERBASE_PROJECT_ID"); v != "" {
		cfg.BrowserbaseProjectID = v
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_BROWSER_INSTANCES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxBrowserInstances = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_BROWSER_RENDER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.BrowserRenderTimeout = d
		}
	}
	if v := os.Getenv("SEO_CRAWLER_RESPECT_ROBOTS"); v != "" {
		parsed, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("parsing SEO_CRAWLER_RESPECT_ROBOTS: %w", err)
		}
		cfg.RespectRobots = parsed
	}
	if v := os.Getenv("SEO_CRAWLER_ROBOTS_UNREACHABLE_POLICY"); v != "" {
		cfg.RobotsUnreachablePolicy = RobotsUnreachablePolicy(v)
	}
	if v := os.Getenv("SEO_CRAWLER_ALLOW_INSECURE_TLS"); v != "" {
		parsed, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("parsing SEO_CRAWLER_ALLOW_INSECURE_TLS: %w", err)
		}
		cfg.AllowInsecureTLS = parsed
	}
	if v := os.Getenv("SEO_CRAWLER_ALLOW_PRIVATE_NETWORKS"); v != "" {
		parsed, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("parsing SEO_CRAWLER_ALLOW_PRIVATE_NETWORKS: %w", err)
		}
		cfg.AllowPrivateNetworks = parsed
	}
	if v := os.Getenv("SEO_CRAWLER_SSRF_PROTECTION"); v != "" {
		parsed, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("parsing SEO_CRAWLER_SSRF_PROTECTION: %w", err)
		}
		cfg.SSRFProtection = parsed
	}
	if v := os.Getenv("SEO_CRAWLER_TITLE_MAX_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.TitleMaxLength = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_TITLE_MIN_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.TitleMinLength = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_DESCRIPTION_MAX_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DescriptionMaxLength = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_DESCRIPTION_MIN_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DescriptionMinLength = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_THIN_CONTENT_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ThinContentThreshold = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_DEEP_PAGE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DeepPageThreshold = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_CONCURRENT_CRAWLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrentCrawls = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_CONCURRENT_ANALYZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrentAnalyze = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_JOBS_PER_HOUR"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxJobsPerHour = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_ANALYZE_JOB_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.AnalyzeJobTTL = d
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_SITEMAP_ENTRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxSitemapEntries = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_QUEUE_MEMORY_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxQueueMemoryMB = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_QUERY_VARIANTS_PER_PATH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxQueryVariantsPerPath = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_FORCE_RENDER_PATTERNS"); v != "" {
		cfg.ForceRenderPatterns = strings.Split(v, ",")
	}
	if v := os.Getenv("SEO_CRAWLER_MAX_JOB_AGE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.MaxJobAge = d
		}
	}
	if v := os.Getenv("SEO_CRAWLER_PSI_API_KEY"); v != "" {
		cfg.PSIAPIKey = v
	}
	if v := os.Getenv("SEO_CRAWLER_PSI_MAX_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PSIMaxPages = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_LANGUAGETOOL_URL"); v != "" {
		cfg.LanguageToolURL = v
	}
	if v := os.Getenv("SEO_CRAWLER_GRAMMAR_MAX_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.GrammarMaxPages = n
		}
	}
	if v := os.Getenv("SEO_CRAWLER_PSI_DESKTOP"); v != "" {
		parsed, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("parsing SEO_CRAWLER_PSI_DESKTOP: %w", err)
		}
		cfg.PSIDesktop = parsed
	}
	if v := os.Getenv("SEO_CRAWLER_AXE_MAX_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.AxeMaxPages = n
		}
	}
	return nil
}

// parseBool parses common boolean strings.
func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean value %q", s)
}

func validateConfig(cfg *Config) error {
	switch cfg.RenderProvider {
	case RenderProviderLocal, RenderProviderBrowserbase, RenderProviderAuto:
	default:
		return fmt.Errorf("invalid render_provider %q", cfg.RenderProvider)
	}
	switch cfg.RobotsUnreachablePolicy {
	case RobotsUnreachablePolicyAllow, RobotsUnreachablePolicyDisallow, RobotsUnreachablePolicyCacheThenAllow:
	default:
		return fmt.Errorf("invalid robots_unreachable_policy %q", cfg.RobotsUnreachablePolicy)
	}
	for _, pattern := range cfg.ForceRenderPatterns {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("invalid force_render_patterns entry %q: %w", pattern, err)
		}
	}
	if cfg.PSIMaxPages < 0 {
		return fmt.Errorf("psi_max_pages must be >= 0")
	}
	if cfg.AxeMaxPages < 0 {
		return fmt.Errorf("axe_max_pages must be >= 0")
	}
	if cfg.GrammarMaxPages < 0 {
		return fmt.Errorf("grammar_max_pages must be >= 0")
	}
	return nil
}
