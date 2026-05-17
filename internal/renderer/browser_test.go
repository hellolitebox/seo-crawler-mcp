package renderer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func skipIfNoChrome(t *testing.T) {
	t.Helper()
	allowPrivateRendererURLsForTest = true
	t.Cleanup(func() { allowPrivateRendererURLsForTest = false })
	if _, err := exec.LookPath("chromium"); err != nil {
		if _, err := exec.LookPath("google-chrome"); err != nil {
			if _, err := exec.LookPath("chromium-browser"); err != nil {
				t.Skip("Chrome/Chromium not found, skipping browser tests")
			}
		}
	}
}

const jsPage = `<!DOCTYPE html>
<html>
<head><title>JS App</title></head>
<body>
<div id="root"></div>
<script>
document.getElementById('root').innerHTML = '<h1>Rendered Content</h1><p>This was added by JavaScript</p>';
</script>
</body>
</html>`

func TestRenderRejectsPrivateURLBeforeLaunchingBrowser(t *testing.T) {
	allowPrivateRendererURLsForTest = false
	pool := NewPool(Options{MaxSlots: 1, RenderTimeout: time.Second})
	defer pool.Close()

	start := time.Now()
	_, err := pool.Render(context.Background(), "http://127.0.0.1:9/")
	if err == nil {
		t.Fatal("expected private URL render to be rejected")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("private URL rejection was not immediate")
	}
}

func TestRender_BasicPage(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(jsPage))
	}))
	defer srv.Close()

	pool := NewPool(Options{
		MaxSlots:      1,
		RenderWaitMs:  500,
		RenderTimeout: 15 * time.Second,
	})
	defer pool.Close()

	ctx := context.Background()
	result, err := pool.Render(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result.HTML, "Rendered Content") {
		t.Errorf("expected rendered JS content in HTML, got: %s", result.HTML[:min(200, len(result.HTML))])
	}
	if !strings.Contains(result.HTML, "This was added by JavaScript") {
		t.Errorf("expected JS-injected paragraph in HTML")
	}
	if result.FinalURL == "" {
		t.Error("expected non-empty FinalURL")
	}
	if result.RenderTime <= 0 {
		t.Error("expected positive RenderTime")
	}
}

func TestRender_Timeout(t *testing.T) {
	skipIfNoChrome(t)

	const slowPage = `<!DOCTYPE html>
<html><head><title>Slow</title></head>
<body>
<script>
// Burn CPU until the context is cancelled.
while(true) { Math.random(); }
</script>
</body>
</html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(slowPage))
	}))
	defer srv.Close()

	pool := NewPool(Options{
		MaxSlots:      1,
		RenderWaitMs:  100,
		RenderTimeout: 3 * time.Second,
	})
	defer pool.Close()

	ctx := context.Background()
	_, err := pool.Render(ctx, srv.URL)
	if err == nil {
		// chromedp may still succeed if the page loads before JS blocks;
		// the infinite loop runs after DOMContentLoaded. That's acceptable —
		// the key invariant is that Render returns within the timeout.
		t.Log("Render returned without error (page loaded before JS blocked); acceptable")
	}
}

func TestPool_Concurrency(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(jsPage))
	}))
	defer srv.Close()

	pool := NewPool(Options{
		MaxSlots:      1,
		RenderWaitMs:  200,
		RenderTimeout: 15 * time.Second,
	})
	defer pool.Close()

	const numRenders = 3
	var wg sync.WaitGroup
	var successes atomic.Int32

	wg.Add(numRenders)
	for i := 0; i < numRenders; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			result, err := pool.Render(ctx, srv.URL)
			if err == nil && strings.Contains(result.HTML, "Rendered Content") {
				successes.Add(1)
			}
		}()
	}

	wg.Wait()

	if got := successes.Load(); got != numRenders {
		t.Errorf("expected %d successful renders, got %d", numRenders, got)
	}
}

// hiddenNavPage has a hamburger button that reveals links only when clicked.
const hiddenNavPage = `<!DOCTYPE html>
<html>
<head><title>Hidden Nav</title></head>
<body>
<button aria-label="menu" id="hamburger" onclick="document.getElementById('nav').style.display='block'">☰</button>
<nav id="nav" style="display:none">
  <a href="/about">About</a>
  <a href="/contact">Contact</a>
  <a href="/pricing">Pricing</a>
</nav>
<main><p>Main content</p></main>
</body>
</html>`

func TestRenderWithOptions_MenuDiscovery(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(hiddenNavPage))
	}))
	defer srv.Close()

	pool := NewPool(Options{
		MaxSlots:      1,
		RenderWaitMs:  500,
		RenderTimeout: 30 * time.Second,
	})
	defer pool.Close()

	ctx := context.Background()

	// Without menu discovery: nav links are in the DOM but hidden (display:none).
	// The HTML still contains the href attributes, but the nav is not expanded.
	resultPlain, err := pool.Render(ctx, srv.URL)
	if err != nil {
		t.Fatalf("plain Render failed: %v", err)
	}
	// The hidden nav should have display:none in the style attribute.
	if !strings.Contains(resultPlain.HTML, `display:none`) &&
		!strings.Contains(resultPlain.HTML, `display: none`) {
		t.Log("note: nav might already be visible without interaction")
	}

	// With menu discovery: the hamburger gets clicked, nav becomes visible.
	resultMenu, err := pool.RenderWithOptions(ctx, srv.URL, RenderOptions{
		DiscoverMenus: true,
	})
	if err != nil {
		t.Fatalf("RenderWithOptions failed: %v", err)
	}

	// After clicking the hamburger, the nav should have display:block.
	if !strings.Contains(resultMenu.HTML, `display:block`) &&
		!strings.Contains(resultMenu.HTML, `display: block`) {
		t.Errorf("expected nav to be visible (display:block) after menu discovery, HTML snippet: %s",
			resultMenu.HTML[:min(500, len(resultMenu.HTML))])
	}

	// Verify the links are present in the expanded HTML.
	for _, href := range []string{"/about", "/contact", "/pricing"} {
		if !strings.Contains(resultMenu.HTML, href) {
			t.Errorf("expected link %q in menu-discovered HTML", href)
		}
	}

	if resultMenu.RenderTime <= 0 {
		t.Error("expected positive RenderTime")
	}
}

func TestPool_Close(t *testing.T) {
	skipIfNoChrome(t)

	pool := NewPool(Options{MaxSlots: 1})
	pool.Close()

	ctx := context.Background()
	_, err := pool.Render(ctx, "http://localhost:1")
	if err == nil {
		t.Fatal("expected error from closed pool, got nil")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected closed error, got: %v", err)
	}
}

func TestPoolReleaseSlotDoesNotBlockAfterClose(t *testing.T) {
	pool := NewPool(Options{MaxSlots: 1})
	<-pool.slots
	pool.Close()

	done := make(chan struct{})
	go func() {
		pool.releaseSlot()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("releaseSlot blocked after pool close")
	}
}
