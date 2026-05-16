package renderer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func skipIfNoPlaywright(t *testing.T) {
	t.Helper()
	if !IsPlaywrightAvailable() {
		t.Skip("Playwright (python3 + playwright package) not available, skipping")
	}
}

// hiddenMenuPage serves a page with a hamburger button that reveals nav links
// only when clicked — exactly the scenario Playwright should handle.
const hiddenMenuPage = `<!DOCTYPE html>
<html>
<head><title>Menu Test</title></head>
<body>
<header>
  <button aria-label="menu" onclick="document.getElementById('menu').style.display='block'">☰ Menu</button>
</header>
<nav id="menu" style="display:none">
  <a href="/services">Services</a>
  <a href="/about">About</a>
  <a href="/contact">Contact</a>
</nav>
<main>
  <a href="/visible-link">Visible Link</a>
</main>
</body>
</html>`

func TestRenderWithPlaywright_MenuDiscovery(t *testing.T) {
	skipIfNoPlaywright(t)
	allowPrivateRendererURLsForTest = true
	defer func() { allowPrivateRendererURLsForTest = false }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(hiddenMenuPage))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := RenderWithPlaywright(ctx, srv.URL)
	if err != nil {
		t.Fatalf("RenderWithPlaywright failed: %v", err)
	}

	// Should have discovered links
	if len(result.Links) == 0 {
		t.Fatal("expected discovered links, got none")
	}

	// The visible link should always be found
	foundVisible := false
	for _, link := range result.Links {
		if strings.HasSuffix(link, "/visible-link") {
			foundVisible = true
			break
		}
	}
	if !foundVisible {
		t.Error("expected /visible-link in discovered links")
	}

	// HTML should be non-empty
	if len(result.HTML) == 0 {
		t.Error("expected non-empty HTML")
	}

	// HTML should contain the nav links (they're in the DOM regardless of visibility)
	for _, href := range []string{"/services", "/about", "/contact"} {
		if !strings.Contains(result.HTML, href) {
			t.Errorf("expected %q in HTML", href)
		}
	}
}

func TestRenderWithPlaywright_BasicPage(t *testing.T) {
	skipIfNoPlaywright(t)
	allowPrivateRendererURLsForTest = true
	defer func() { allowPrivateRendererURLsForTest = false }()

	const basicPage = `<!DOCTYPE html>
<html>
<head><title>Basic</title></head>
<body>
<a href="/page1">Page 1</a>
<a href="/page2">Page 2</a>
</body>
</html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(basicPage))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := RenderWithPlaywright(ctx, srv.URL)
	if err != nil {
		t.Fatalf("RenderWithPlaywright failed: %v", err)
	}

	if len(result.Links) < 2 {
		t.Errorf("expected at least 2 links, got %d", len(result.Links))
	}

	if !strings.Contains(result.HTML, "Page 1") {
		t.Error("expected 'Page 1' in HTML")
	}
}

func TestIsPlaywrightAvailable(t *testing.T) {
	// This just tests that the function doesn't panic and returns a bool.
	available := IsPlaywrightAvailable()
	t.Logf("Playwright available: %v", available)
}

func TestClampPlaywrightResultLimitsHTMLAndLinks(t *testing.T) {
	links := make([]string, maxPlaywrightLinks+10)
	for i := range links {
		links[i] = "https://example.com/page"
	}
	result := &PlaywrightResult{
		HTML:  strings.Repeat("x", maxPlaywrightHTMLBytes+10),
		Links: links,
	}

	clampPlaywrightResult(result)

	if len(result.HTML) != maxPlaywrightHTMLBytes {
		t.Fatalf("HTML length = %d, want %d", len(result.HTML), maxPlaywrightHTMLBytes)
	}
	if len(result.Links) != maxPlaywrightLinks {
		t.Fatalf("links length = %d, want %d", len(result.Links), maxPlaywrightLinks)
	}
}

func TestValidatePlaywrightResultRejectsPrivateFinalURL(t *testing.T) {
	result := &PlaywrightResult{FinalURL: "http://127.0.0.1/admin"}
	if err := validatePlaywrightResult("https://example.com/", result); err == nil {
		t.Fatal("expected private final URL to be rejected")
	}
}

func TestRunPythonJSONRejectsOversizedOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runPythonJSON(ctx, "import sys; sys.stdout.write('x' * (12 * 1024 * 1024 + 1))")
	if err == nil {
		t.Fatal("expected oversized output error")
	}
	if !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}
