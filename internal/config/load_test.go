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
	if cfg.RespectRobots != false {
		t.Errorf("RespectRobots = %v, want false", cfg.RespectRobots)
	}

	// Verify defaults are preserved for unset fields.
	defaults := DefaultConfig()
	if cfg.MaxDepth != defaults.MaxDepth {
		t.Errorf("MaxDepth = %d, want default %d", cfg.MaxDepth, defaults.MaxDepth)
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
