package renderer

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestIsBrowserbaseConfigured(t *testing.T) {
	if IsBrowserbaseConfigured("") {
		t.Fatal("empty API key should not configure Browserbase")
	}
	if !IsBrowserbaseConfigured("bb_test") {
		t.Fatal("non-empty API key should configure Browserbase")
	}
}

func TestRenderWithBrowserbaseLive(t *testing.T) {
	if os.Getenv("BROWSERBASE_LIVE_TEST") != "1" {
		t.Skip("set BROWSERBASE_LIVE_TEST=1 to run live Browserbase smoke test")
	}
	apiKey := os.Getenv("BROWSERBASE_API_KEY")
	if apiKey == "" {
		t.Fatal("BROWSERBASE_API_KEY is required for live Browserbase smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	result, err := RenderPageContentOnlyWithBrowserbase(ctx, "https://example.com/", BrowserbaseOptions{
		APIKey:        apiKey,
		ProjectID:     os.Getenv("BROWSERBASE_PROJECT_ID"),
		RenderWaitMs:  500,
		RenderTimeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("RenderPageContentOnlyWithBrowserbase: %v", err)
	}
	if result.FinalURL == "" || result.HTML == "" {
		t.Fatalf("Browserbase result missing final URL or HTML: %+v", result)
	}
}
