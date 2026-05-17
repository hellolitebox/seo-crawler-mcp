package fetcher

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
)

// Options configures a Fetcher instance.
type Options struct {
	UserAgent           string
	Timeout             time.Duration
	MaxResponseBody     int64
	MaxDecompressedBody int64
	MaxRedirectHops     int
	Retries             int
	AllowInsecureTLS    bool
	SSRFGuard           *ssrf.Guard
}

// FetchResult holds the outcome of an HTTP request.
type FetchResult struct {
	RequestedURL         string
	FinalURL             string
	StatusCode           int
	ContentType          string
	ContentEncoding      string
	ResponseHeaders      http.Header
	Body                 []byte
	BodySize             int64
	TTFBMS               int64
	RedirectHops         []RedirectHop
	RedirectLoopDetected bool
	RedirectHopsExceeded bool
	Error                error
}

// RedirectHop records a single redirect in the chain.
type RedirectHop struct {
	HopIndex   int
	StatusCode int
	FromURL    string
	ToURL      string
}

// Fetcher performs HTTP requests with SSRF protection, redirect tracking,
// decompression, and TTFB measurement.
type Fetcher struct {
	opts        Options
	transport   *http.Transport
	RateLimiter *RateLimiter
}

// New creates a Fetcher with the given options and a shared transport.
func New(opts Options) *Fetcher {
	dialTimeout := opts.Timeout
	if dialTimeout <= 0 {
		dialTimeout = 30 * time.Second
	}
	baseDialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:               nil,
		DisableCompression:  true, // We handle decompression manually.
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if opts.SSRFGuard != nil {
				if err := opts.SSRFGuard.ValidateHost(host); err != nil {
					return nil, err
				}
			}
			conn, err := baseDialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if opts.SSRFGuard != nil {
				if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok && opts.SSRFGuard.IsBlockedIP(tcp.IP) {
					_ = conn.Close()
					return nil, fmt.Errorf("ssrf: dial resolved to blocked IP %s", tcp.IP)
				}
			}
			return conn, nil
		},
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: opts.AllowInsecureTLS, //nolint:gosec
			MinVersion:         tls.VersionTLS12,
		},
	}

	return &Fetcher{
		opts:      opts,
		transport: transport,
	}
}

// SafeClient returns an *http.Client that uses the fetcher's SSRF-protected
// transport and timeout. Use this when you need a plain client that still
// honours the same security constraints as Fetch().
func (f *Fetcher) SafeClient() *http.Client {
	return &http.Client{
		Transport: f.transport,
		Timeout:   f.opts.Timeout,
	}
}

// Fetch performs an HTTP GET request.
func (f *Fetcher) Fetch(rawURL string) (*FetchResult, error) {
	return f.FetchContext(context.Background(), rawURL)
}

// Head performs an HTTP HEAD request.
func (f *Fetcher) Head(rawURL string) (*FetchResult, error) {
	return f.HeadContext(context.Background(), rawURL)
}

// FetchContext performs an HTTP GET request with cancellation support.
func (f *Fetcher) FetchContext(ctx context.Context, rawURL string) (*FetchResult, error) {
	return f.do(ctx, rawURL, http.MethodGet)
}

// HeadContext performs an HTTP HEAD request with cancellation support.
func (f *Fetcher) HeadContext(ctx context.Context, rawURL string) (*FetchResult, error) {
	return f.do(ctx, rawURL, http.MethodHead)
}

// parseRetryAfter parses a Retry-After header value as either seconds or an
// HTTP-date and returns the duration to wait. Returns 0 on parse failure.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	// Try parsing as seconds first.
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// Try parsing as HTTP-date (RFC 1123).
	if t, err := time.Parse(time.RFC1123, value); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	// Try RFC 850 format.
	if t, err := time.Parse(time.RFC850, value); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// maxRetryAfter caps the wait time for 429 retries.
const maxRetryAfter = 60 * time.Second

func (f *Fetcher) do(ctx context.Context, rawURL string, method string) (*FetchResult, error) {
	result := &FetchResult{
		RequestedURL: rawURL,
		RedirectHops: make([]RedirectHop, 0),
	}

	// SSRF: validate URL scheme.
	if f.opts.SSRFGuard != nil {
		if err := f.opts.SSRFGuard.ValidateURL(rawURL); err != nil {
			return nil, fmt.Errorf("fetcher: SSRF check failed for %q: %w", rawURL, err)
		}
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("fetcher: invalid URL %q: %w", rawURL, err)
		}
		if err := f.opts.SSRFGuard.ValidateHost(parsed.Hostname()); err != nil {
			return nil, fmt.Errorf("fetcher: SSRF check failed for host %q: %w", parsed.Hostname(), err)
		}
	}

	// Track seen URLs for loop detection.
	seen := map[string]bool{rawURL: true}

	// Per-request client to avoid race on CheckRedirect state.
	client := &http.Client{
		Transport: f.transport,
		Timeout:   f.opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			fromURL := via[len(via)-1].URL.String()
			toURL := req.URL.String()
			statusCode := req.Response.StatusCode

			hop := RedirectHop{
				HopIndex:   len(result.RedirectHops),
				StatusCode: statusCode,
				FromURL:    fromURL,
				ToURL:      toURL,
			}
			result.RedirectHops = append(result.RedirectHops, hop)

			// Set User-Agent on redirected request.
			if f.opts.UserAgent != "" {
				req.Header.Set("User-Agent", f.opts.UserAgent)
			}

			// Loop detection.
			if seen[toURL] {
				result.RedirectLoopDetected = true
				return http.ErrUseLastResponse
			}
			seen[toURL] = true

			// Max hops check.
			if len(result.RedirectHops) >= f.opts.MaxRedirectHops {
				result.RedirectHopsExceeded = true
				return http.ErrUseLastResponse
			}

			// SSRF re-validation on redirect.
			if f.opts.SSRFGuard != nil {
				if err := f.opts.SSRFGuard.ValidateURL(toURL); err != nil {
					return fmt.Errorf("fetcher: SSRF check on redirect to %q: %w", toURL, err)
				}
				if err := f.opts.SSRFGuard.ValidateHost(req.URL.Hostname()); err != nil {
					return fmt.Errorf("fetcher: SSRF check on redirect host %q: %w", req.URL.Hostname(), err)
				}
			}

			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetcher: failed to create request for %q: %w", rawURL, err)
	}
	if f.opts.UserAgent != "" {
		req.Header.Set("User-Agent", f.opts.UserAgent)
	}
	// Ask servers to send compressed responses (we decompress manually).
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	start := time.Now()
	resp, err := client.Do(req)
	result.TTFBMS = time.Since(start).Milliseconds()

	if err != nil {
		result.Error = err
		return result, err
	}

	// Handle 429 Too Many Requests: retry once after waiting.
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()

		wait := parseRetryAfter(resp.Header.Get("Retry-After"))
		if wait == 0 {
			// Exponential backoff: use Retries count as attempt indicator.
			// Default: 1s for first 429.
			wait = time.Duration(math.Pow(2, float64(f.opts.Retries))) * time.Second
			if wait > maxRetryAfter {
				wait = maxRetryAfter
			}
		}
		if wait > maxRetryAfter {
			wait = maxRetryAfter
		}

		// Throttle the host via rate limiter if available.
		if f.RateLimiter != nil {
			if u, pErr := url.Parse(rawURL); pErr == nil {
				f.RateLimiter.ThrottleHost(u.Hostname(), wait)
			}
		}

		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			return result, ctx.Err()
		case <-time.After(wait):
		}

		// Rebuild request for retry.
		retryReq, retryErr := http.NewRequestWithContext(ctx, method, rawURL, nil)
		if retryErr != nil {
			result.Error = retryErr
			return result, retryErr
		}
		if f.opts.UserAgent != "" {
			retryReq.Header.Set("User-Agent", f.opts.UserAgent)
		}
		retryReq.Header.Set("Accept-Encoding", "gzip, deflate")

		retryStart := time.Now()
		resp, err = client.Do(retryReq)
		result.TTFBMS = time.Since(retryStart).Milliseconds()
		if err != nil {
			result.Error = err
			return result, err
		}
	}

	defer resp.Body.Close()

	result.FinalURL = resp.Request.URL.String()
	result.StatusCode = resp.StatusCode
	result.ResponseHeaders = resp.Header
	result.ContentEncoding = resp.Header.Get("Content-Encoding")

	// Extract media type without charset params.
	ct := resp.Header.Get("Content-Type")
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = ct[:idx]
	}
	result.ContentType = strings.TrimSpace(ct)

	// Skip body reading for HEAD.
	if method == http.MethodHead {
		return result, nil
	}

	// Limit raw body read.
	limited := io.LimitReader(resp.Body, f.opts.MaxResponseBody)

	// Decompress if needed.
	maxDecomp := f.opts.MaxDecompressedBody
	if maxDecomp == 0 {
		maxDecomp = f.opts.MaxResponseBody
	}
	reader, err := decompressAndLimit(limited, result.ContentEncoding, maxDecomp)
	if err != nil {
		result.Error = err
		return result, err
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		result.Error = err
		return result, err
	}

	result.Body = body
	result.BodySize = int64(len(body))

	return result, nil
}
