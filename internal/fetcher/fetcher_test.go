package fetcher

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
)

func defaultOpts() Options {
	return Options{
		UserAgent:           "TestBot/1.0",
		Timeout:             5 * time.Second,
		MaxResponseBody:     1 << 20, // 1MB
		MaxDecompressedBody: 2 << 20, // 2MB
		MaxRedirectHops:     10,
	}
}

func TestFetchBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html>hello</html>")
	}))
	defer srv.Close()

	f := New(defaultOpts())
	result, err := f.Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if result.ContentType != "text/html" {
		t.Errorf("ContentType = %q, want %q", result.ContentType, "text/html")
	}
	if string(result.Body) != "<html>hello</html>" {
		t.Errorf("Body = %q, want %q", result.Body, "<html>hello</html>")
	}
	if result.TTFBMS < 0 {
		t.Errorf("TTFBMS = %d, want >= 0", result.TTFBMS)
	}
	if result.RequestedURL != srv.URL {
		t.Errorf("RequestedURL = %q, want %q", result.RequestedURL, srv.URL)
	}
}

func TestFetchRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "/step1", http.StatusFound)
		case "/step1":
			http.Redirect(w, r, "/step2", http.StatusMovedPermanently)
		case "/step2":
			fmt.Fprint(w, "final")
		}
	}))
	defer srv.Close()

	f := New(defaultOpts())
	result, err := f.Fetch(srv.URL + "/")
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	if len(result.RedirectHops) != 2 {
		t.Fatalf("RedirectHops = %d, want 2", len(result.RedirectHops))
	}
	if result.RedirectHops[0].StatusCode != http.StatusFound {
		t.Errorf("hop[0] status = %d, want %d", result.RedirectHops[0].StatusCode, http.StatusFound)
	}
	if result.RedirectHops[1].StatusCode != http.StatusMovedPermanently {
		t.Errorf("hop[1] status = %d, want %d", result.RedirectHops[1].StatusCode, http.StatusMovedPermanently)
	}
	if !strings.HasSuffix(result.FinalURL, "/step2") {
		t.Errorf("FinalURL = %q, want suffix /step2", result.FinalURL)
	}
	if string(result.Body) != "final" {
		t.Errorf("Body = %q, want %q", result.Body, "final")
	}
}

func TestFetchMaxRedirectHops(t *testing.T) {
	counter := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter++
		http.Redirect(w, r, fmt.Sprintf("/r%d", counter), http.StatusFound)
	}))
	defer srv.Close()

	opts := defaultOpts()
	opts.MaxRedirectHops = 3
	f := New(opts)

	result, err := f.Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	if !result.RedirectHopsExceeded {
		t.Error("expected RedirectHopsExceeded = true")
	}
}

func TestFetchUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	opts := defaultOpts()
	opts.UserAgent = "SEO-Crawler/2.0"
	f := New(opts)

	_, err := f.Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	if gotUA != "SEO-Crawler/2.0" {
		t.Errorf("User-Agent = %q, want %q", gotUA, "SEO-Crawler/2.0")
	}
}

func TestFetchHeadOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %q, want HEAD", r.Method)
		}
		w.Header().Set("Content-Type", "text/plain")
		// Write body (won't be sent for HEAD, but tests the server side).
		fmt.Fprint(w, "this body should not be read")
	}))
	defer srv.Close()

	f := New(defaultOpts())
	result, err := f.Head(srv.URL)
	if err != nil {
		t.Fatalf("Head error: %v", err)
	}

	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if len(result.Body) != 0 {
		t.Errorf("Body len = %d, want 0 for HEAD", len(result.Body))
	}
}

func TestFetchSSRF(t *testing.T) {
	guard := ssrf.NewGuard(false)
	opts := defaultOpts()
	opts.SSRFGuard = guard

	f := New(opts)

	// Attempt to fetch a loopback address — should be blocked.
	_, err := f.Fetch("http://127.0.0.1:9999/evil")
	if err == nil {
		t.Fatal("expected error for SSRF-blocked request, got nil")
	}
	if !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "ssrf") {
		t.Errorf("error = %q, want it to mention SSRF", err)
	}
}

func TestFetchContextCancelsSlowRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			fmt.Fprint(w, "too late")
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	f := New(defaultOpts())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := f.FetchContext(ctx, srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected cancelled fetch error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("FetchContext error = %v, want deadline cancellation", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("FetchContext returned after %v, want prompt cancellation", elapsed)
	}
}

func TestFetchSSRFBlocksDirectPrivateIPsBeforeDial(t *testing.T) {
	guard := ssrf.NewGuard(false)
	opts := defaultOpts()
	opts.SSRFGuard = guard
	opts.Timeout = 5 * time.Second
	f := New(opts)

	for _, rawURL := range []string{
		"http://10.0.0.1/",
		"http://192.168.0.1/",
		"http://169.254.169.254/latest/meta-data/",
	} {
		t.Run(rawURL, func(t *testing.T) {
			start := time.Now()
			_, err := f.Fetch(rawURL)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatal("expected SSRF error, got nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "ssrf") {
				t.Fatalf("error = %q, want SSRF", err)
			}
			if elapsed > 200*time.Millisecond {
				t.Fatalf("blocked direct private IP after %v, want pre-dial failure", elapsed)
			}
		})
	}
}

func TestFetch429RetryWithRetryAfter(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, "rate limited")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>ok</html>")
	}))
	defer srv.Close()

	f := New(defaultOpts())
	start := time.Now()
	result, err := f.Fetch(srv.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts (original + retry), got %d", attempts)
	}
	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200 after retry", result.StatusCode)
	}
	if string(result.Body) != "<html>ok</html>" {
		t.Errorf("Body = %q, want %q", result.Body, "<html>ok</html>")
	}
	// Should have waited at least 1 second for Retry-After.
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, expected >= ~1s for Retry-After", elapsed)
	}
}

func TestFetch429RetryWithExponentialBackoff(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			// No Retry-After header -> should use exponential backoff.
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	opts := defaultOpts()
	opts.Retries = 0 // 2^0 = 1 second backoff
	f := New(opts)

	start := time.Now()
	result, err := f.Fetch(srv.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if elapsed < 800*time.Millisecond {
		t.Errorf("elapsed = %v, expected >= ~1s for backoff", elapsed)
	}
}

func TestFetch429PersistentReturns429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "still rate limited")
	}))
	defer srv.Close()

	f := New(defaultOpts())
	result, err := f.Fetch(srv.URL)

	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	// After retry, if still 429, return that status.
	if result.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429 after failed retry", result.StatusCode)
	}
}

func TestFetch429ThrottlesHost(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	rl := NewRateLimiter(2)
	f := New(defaultOpts())
	f.RateLimiter = rl

	result, err := f.Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
}

func TestFetchGzipBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		gw := gzip.NewWriter(w)
		fmt.Fprint(gw, "<html>compressed</html>")
		gw.Close()
	}))
	defer srv.Close()

	f := New(defaultOpts())
	result, err := f.Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	if string(result.Body) != "<html>compressed</html>" {
		t.Errorf("Body = %q, want %q", result.Body, "<html>compressed</html>")
	}
	if result.ContentEncoding != "gzip" {
		t.Errorf("ContentEncoding = %q, want %q", result.ContentEncoding, "gzip")
	}
}
