package textquality

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestLTClient_CheckUsesDefaultClientWhenNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/check" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"language":{"detectedLanguage":{"name":"English","code":"en"}},"matches":[]}`)
	}))
	defer server.Close()

	client := &LTClient{BaseURL: server.URL}
	if _, err := client.Check(context.Background(), "plain text", "en-US"); err != nil {
		t.Fatalf("Check failed with nil HTTPClient: %v", err)
	}
}

func TestSubstringByRuneOffset(t *testing.T) {
	got, ok := substringByRuneOffset("ab caf\u00e9", 3, 4)
	if !ok {
		t.Fatal("substringByRuneOffset returned !ok")
	}
	if got != "caf\u00e9" {
		t.Fatalf("substring = %q, want cafe with accent", got)
	}
}
