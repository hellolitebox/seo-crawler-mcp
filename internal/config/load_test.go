package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig with no file: %v", err)
	}

	defaults := DefaultConfig()
	if cfg.DBPath != defaults.DBPath {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, defaults.DBPath)
	}
	if cfg.MaxPages != defaults.MaxPages {
		t.Errorf("MaxPages = %d, want %d", cfg.MaxPages, defaults.MaxPages)
	}
	if cfg.UserAgent != defaults.UserAgent {
		t.Errorf("UserAgent = %q, want %q", cfg.UserAgent, defaults.UserAgent)
	}
	if cfg.ScopeMode != defaults.ScopeMode {
		t.Errorf("ScopeMode = %q, want %q", cfg.ScopeMode, defaults.ScopeMode)
	}
	if cfg.RenderProvider != defaults.RenderProvider {
		t.Errorf("RenderProvider = %q, want %q", cfg.RenderProvider, defaults.RenderProvider)
	}
	if cfg.RequestTimeout != defaults.RequestTimeout {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, defaults.RequestTimeout)
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	content := `
db_path = "custom.db"
max_pages = 500
user_agent = "test-bot/1.0"
scope_mode = "exact_host"
request_timeout = "5s"
render_mode = "static"
render_provider = "browserbase"
browserbase_api_key = "bb_test"
browserbase_project_id = "project_test"
psi_max_pages = 0
axe_max_pages = 12
grammar_max_pages = 8
respect_robots = false
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig from file: %v", err)
	}

	if cfg.DBPath != "custom.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "custom.db")
	}
	if cfg.MaxPages != 500 {
		t.Errorf("MaxPages = %d, want 500", cfg.MaxPages)
	}
	if cfg.UserAgent != "test-bot/1.0" {
		t.Errorf("UserAgent = %q, want %q", cfg.UserAgent, "test-bot/1.0")
	}
	if cfg.ScopeMode != ScopeModeExactHost {
		t.Errorf("ScopeMode = %q, want %q", cfg.ScopeMode, ScopeModeExactHost)
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Errorf("RequestTimeout = %v, want 5s", cfg.RequestTimeout)
	}
	if cfg.RenderMode != RenderModeStatic {
		t.Errorf("RenderMode = %q, want %q", cfg.RenderMode, RenderModeStatic)
	}
	if cfg.RenderProvider != RenderProviderBrowserbase {
		t.Errorf("RenderProvider = %q, want %q", cfg.RenderProvider, RenderProviderBrowserbase)
	}
	if cfg.BrowserbaseAPIKey != "bb_test" {
		t.Errorf("BrowserbaseAPIKey = %q, want bb_test", cfg.BrowserbaseAPIKey)
	}
	if cfg.BrowserbaseProjectID != "project_test" {
		t.Errorf("BrowserbaseProjectID = %q, want project_test", cfg.BrowserbaseProjectID)
	}
	if cfg.PSIMaxPages != 0 {
		t.Errorf("PSIMaxPages = %d, want 0", cfg.PSIMaxPages)
	}
	if cfg.AxeMaxPages != 12 {
		t.Errorf("AxeMaxPages = %d, want 12", cfg.AxeMaxPages)
	}
	if cfg.GrammarMaxPages != 8 {
		t.Errorf("GrammarMaxPages = %d, want 8", cfg.GrammarMaxPages)
	}
	if cfg.RespectRobots != false {
		t.Errorf("RespectRobots = %v, want false", cfg.RespectRobots)
	}

	// Verify defaults are preserved for unset fields.
	defaults := DefaultConfig()
	if cfg.MaxDepth != defaults.MaxDepth {
		t.Errorf("MaxDepth = %d, want default %d", cfg.MaxDepth, defaults.MaxDepth)
	}
}

func TestLoadConfig_URLGroupsFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	content := `
[[url_groups]]
name = "docs"
pattern = "/docs/*"
thinContentThreshold = 120
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig from file: %v", err)
	}
	if len(cfg.URLGroups) != 1 {
		t.Fatalf("URLGroups len = %d, want 1", len(cfg.URLGroups))
	}
	if cfg.URLGroups[0].Name != "docs" || cfg.URLGroups[0].Pattern != "/docs/*" {
		t.Fatalf("URLGroups[0] = %+v", cfg.URLGroups[0])
	}
	if cfg.URLGroups[0].ThinContentThreshold == nil || *cfg.URLGroups[0].ThinContentThreshold != 120 {
		t.Fatalf("ThinContentThreshold = %v, want 120", cfg.URLGroups[0].ThinContentThreshold)
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	content := `
db_path = "file.db"
max_pages = 100
user_agent = "file-bot"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	// Set env vars that should override file values.
	t.Setenv("SEO_CRAWLER_DB_PATH", "env.db")
	t.Setenv("SEO_CRAWLER_MAX_PAGES", "999")
	t.Setenv("SEO_CRAWLER_SCOPE_MODE", "allowlist")
	t.Setenv("SEO_CRAWLER_RENDER_PROVIDER", "local")
	t.Setenv("BROWSERBASE_API_KEY", "bb_env")
	t.Setenv("BROWSERBASE_PROJECT_ID", "project_env")
	t.Setenv("SEO_CRAWLER_PSI_MAX_PAGES", "7")
	t.Setenv("SEO_CRAWLER_AXE_MAX_PAGES", "0")
	t.Setenv("SEO_CRAWLER_GRAMMAR_MAX_PAGES", "11")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig with env override: %v", err)
	}

	if cfg.DBPath != "env.db" {
		t.Errorf("DBPath = %q, want %q (env override)", cfg.DBPath, "env.db")
	}
	if cfg.MaxPages != 999 {
		t.Errorf("MaxPages = %d, want 999 (env override)", cfg.MaxPages)
	}
	if cfg.ScopeMode != ScopeModeAllowlist {
		t.Errorf("ScopeMode = %q, want %q (env override)", cfg.ScopeMode, ScopeModeAllowlist)
	}
	if cfg.RenderProvider != RenderProviderLocal {
		t.Errorf("RenderProvider = %q, want %q (env override)", cfg.RenderProvider, RenderProviderLocal)
	}
	if cfg.BrowserbaseAPIKey != "bb_env" {
		t.Errorf("BrowserbaseAPIKey = %q, want bb_env (env override)", cfg.BrowserbaseAPIKey)
	}
	if cfg.BrowserbaseProjectID != "project_env" {
		t.Errorf("BrowserbaseProjectID = %q, want project_env (env override)", cfg.BrowserbaseProjectID)
	}
	if cfg.PSIMaxPages != 7 {
		t.Errorf("PSIMaxPages = %d, want 7 (env override)", cfg.PSIMaxPages)
	}
	if cfg.AxeMaxPages != 0 {
		t.Errorf("AxeMaxPages = %d, want 0 (env override)", cfg.AxeMaxPages)
	}
	if cfg.GrammarMaxPages != 11 {
		t.Errorf("GrammarMaxPages = %d, want 11 (env override)", cfg.GrammarMaxPages)
	}
	// UserAgent should remain from file since no env var set.
	if cfg.UserAgent != "file-bot" {
		t.Errorf("UserAgent = %q, want %q (from file)", cfg.UserAgent, "file-bot")
	}
}

func TestLoadFromFile_InvalidPath(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/config.toml")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadFromFile_InvalidDuration(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.toml")

	content := `request_timeout = "not-a-duration"`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	_, err := LoadFromFile(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
}

func TestLoadConfigRejectsInvalidBooleanEnv(t *testing.T) {
	t.Setenv("SEO_CRAWLER_SSRF_PROTECTION", "treu")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected invalid boolean env value to return an error")
	}
}

func TestLoadConfigRejectsInvalidRobotsPolicy(t *testing.T) {
	t.Setenv("SEO_CRAWLER_ROBOTS_UNREACHABLE_POLICY", "disalow")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected invalid robots policy to return an error")
	}
}

func TestLoadConfigRejectsInvalidRenderProvider(t *testing.T) {
	t.Setenv("SEO_CRAWLER_RENDER_PROVIDER", "cloud")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected invalid render provider to return an error")
	}
}

func TestLoadConfigRejectsNegativeAuditLimits(t *testing.T) {
	t.Setenv("SEO_CRAWLER_PSI_MAX_PAGES", "-1")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected negative PSI max pages to return an error")
	}

	t.Setenv("SEO_CRAWLER_PSI_MAX_PAGES", "50")
	t.Setenv("SEO_CRAWLER_AXE_MAX_PAGES", "-1")
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected negative Axe max pages to return an error")
	}

	t.Setenv("SEO_CRAWLER_AXE_MAX_PAGES", "50")
	t.Setenv("SEO_CRAWLER_GRAMMAR_MAX_PAGES", "-1")
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected negative Grammar max pages to return an error")
	}
}

func TestLoadFromFileRejectsInvalidForceRenderPattern(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad-pattern.toml")
	if err := os.WriteFile(cfgPath, []byte("force_render_patterns = [\"/app/[\"]"), 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	if _, err := LoadFromFile(cfgPath); err == nil {
		t.Fatal("expected invalid force_render_patterns entry to return an error")
	}
}
