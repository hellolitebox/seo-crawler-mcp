package config

import (
	"fmt"
	"os"
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

	MaxPages int `toml:"max_pages"`
	MaxDepth int `toml:"max_depth"`

	RenderMode           string `toml:"render_mode"`
	RenderWaitMs         int    `toml:"render_wait_ms"`
	MaxBrowserInstances  int    `toml:"max_browser_instances"`
	BrowserRenderTimeout string `toml:"browser_render_timeout"`
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

	MaxQueueMemoryMB int    `toml:"max_queue_memory_mb"`
	DBPath           string `toml:"db_path"`
	MaxJobAge        string `toml:"max_job_age"`
	PSIAPIKey        string `toml:"psi_api_key"`
	PSIDesktop       bool   `toml:"psi_desktop"`
	LanguageToolURL  string `toml:"languagetool_url"`
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

	applyEnvOverrides(&cfg)
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
	if tc.RenderMode != "" {
		cfg.RenderMode = RenderMode(tc.RenderMode)
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
	if tc.PSIDesktop {
		cfg.PSIDesktop = true
	}
	if tc.LanguageToolURL != "" {
		cfg.LanguageToolURL = tc.LanguageToolURL
	}

	return &cfg, nil
}

// applyEnvOverrides reads SEO_CRAWLER_ prefixed env vars and overrides config values.
func applyEnvOverrides(cfg *Config) {
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
	if v := os.Getenv("SEO_CRAWLER_RENDER_WAIT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RenderWaitMs = n
		}
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
		cfg.RespectRobots = parseBool(v)
	}
	if v := os.Getenv("SEO_CRAWLER_ROBOTS_UNREACHABLE_POLICY"); v != "" {
		cfg.RobotsUnreachablePolicy = RobotsUnreachablePolicy(v)
	}
	if v := os.Getenv("SEO_CRAWLER_ALLOW_INSECURE_TLS"); v != "" {
		cfg.AllowInsecureTLS = parseBool(v)
	}
	if v := os.Getenv("SEO_CRAWLER_ALLOW_PRIVATE_NETWORKS"); v != "" {
		cfg.AllowPrivateNetworks = parseBool(v)
	}
	if v := os.Getenv("SEO_CRAWLER_SSRF_PROTECTION"); v != "" {
		cfg.SSRFProtection = parseBool(v)
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
	if v := os.Getenv("SEO_CRAWLER_LANGUAGETOOL_URL"); v != "" {
		cfg.LanguageToolURL = v
	}
	if v := os.Getenv("SEO_CRAWLER_PSI_DESKTOP"); v != "" {
		cfg.PSIDesktop = parseBool(v)
	}
}

// parseBool parses common boolean strings.
func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
