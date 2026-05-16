package main

import (
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
)

func TestNewPurgeFlagSetDefaultsToEffectiveDBPath(t *testing.T) {
	fs, _, _, dbPath := newPurgeFlagSet("/tmp/effective.db")
	if err := fs.Parse([]string{"--older-than", "30d"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *dbPath != "/tmp/effective.db" {
		t.Fatalf("db path = %q, want effective default", *dbPath)
	}
}

func TestNewPurgeFlagSetAllowsLocalDBOverride(t *testing.T) {
	fs, _, _, dbPath := newPurgeFlagSet("/tmp/effective.db")
	if err := fs.Parse([]string{"--older-than", "30d", "--db", "/tmp/local.db"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *dbPath != "/tmp/local.db" {
		t.Fatalf("db path = %q, want local override", *dbPath)
	}
}

func TestNewSSRFGuardHonorsDisabledProtection(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SSRFProtection = false
	if guard := newSSRFGuard(&cfg); guard != nil {
		t.Fatal("expected nil guard when SSRFProtection=false")
	}
}
