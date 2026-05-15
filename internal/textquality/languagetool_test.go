package textquality

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLTClient_Check_Live(t *testing.T) {
	client := NewLTClient("http://localhost:8010")
	ctx := context.Background()

	if !client.IsAvailable(ctx) {
		t.Skip("LanguageTool not available at localhost:8010")
	}

	result, err := client.Check(ctx, "This is a tset of the langauge tool.", "en-US")
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if len(result.Matches) == 0 {
		t.Fatal("Expected at least one match for misspelled text")
	}

	foundTset := false
	for _, m := range result.Matches {
		if m.ShortMessage == "Spelling mistake" || m.RuleCategory == "Possible Typo" {
			foundTset = true
		}
	}
	if !foundTset {
		t.Errorf("Expected to find spelling mistake, got: %+v", result.Matches)
	}
}

func TestLTClient_Check_EmptyText(t *testing.T) {
	client := NewLTClient("http://localhost:8010")
	ctx := context.Background()

	result, err := client.Check(ctx, "", "en-US")
	if err != nil {
		t.Fatalf("Check on empty text failed: %v", err)
	}
	if len(result.Matches) != 0 {
		t.Errorf("Expected no matches for empty text, got %d", len(result.Matches))
	}
}

func TestLTClient_Check_TruncatesUnicodeAtRuneBoundary(t *testing.T) {
	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		captured = r.Form.Get("text")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"language":{"detectedLanguage":{"name":"English","code":"en-US"}},"matches":[]}`))
	}))
	defer server.Close()

	client := NewLTClient(server.URL)
	text := strings.Repeat("a", 19999) + "é" + strings.Repeat("b", 10)
	if _, err := client.Check(context.Background(), text, "en-US"); err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if !utf8.ValidString(captured) {
		t.Fatalf("captured text is not valid UTF-8")
	}
	if got := utf8.RuneCountInString(captured); got != 20000 {
		t.Fatalf("captured rune count = %d, want 20000", got)
	}
	if !strings.HasSuffix(captured, "é") {
		t.Fatalf("expected truncation to preserve final rune, got suffix %q", captured[len(captured)-2:])
	}
}
