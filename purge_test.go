package main

import "testing"

func TestPurgeUsesGlobalDBPathAsDefault(t *testing.T) {
	fs, _, _, dbPath := newPurgeFlagSet("/tmp/custom.db")

	if err := fs.Parse([]string{"--job", "abc"}); err != nil {
		t.Fatalf("parse purge flags: %v", err)
	}

	if got, want := *dbPath, "/tmp/custom.db"; got != want {
		t.Fatalf("db path = %q, want %q", got, want)
	}
}

func TestPurgeDBFlagOverridesGlobalDBPath(t *testing.T) {
	fs, _, _, dbPath := newPurgeFlagSet("/tmp/custom.db")

	if err := fs.Parse([]string{"--db", "/tmp/other.db", "--job", "abc"}); err != nil {
		t.Fatalf("parse purge flags: %v", err)
	}

	if got, want := *dbPath, "/tmp/other.db"; got != want {
		t.Fatalf("db path = %q, want %q", got, want)
	}
}

func TestPurgeFallsBackToDefaultDBPath(t *testing.T) {
	fs, _, _, dbPath := newPurgeFlagSet("")

	if err := fs.Parse([]string{"--job", "abc"}); err != nil {
		t.Fatalf("parse purge flags: %v", err)
	}

	if got, want := *dbPath, "seo-crawler.db"; got != want {
		t.Fatalf("db path = %q, want %q", got, want)
	}
}
