package textquality

import (
	"context"
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
