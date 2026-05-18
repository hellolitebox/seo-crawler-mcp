package renderer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const browserbaseAPIURL = "https://api.browserbase.com/v1/sessions"

// BrowserbaseOptions configures a single Browserbase-backed render.
type BrowserbaseOptions struct {
	APIKey        string
	ProjectID     string
	RenderWaitMs  int
	RenderTimeout time.Duration
}

func IsBrowserbaseConfigured(apiKey string) bool {
	return apiKey != ""
}

func RenderWithBrowserbase(ctx context.Context, pageURL string, opts BrowserbaseOptions) (*PlaywrightResult, error) {
	return renderBrowserbase(ctx, pageURL, opts, true)
}

func RenderPageContentOnlyWithBrowserbase(ctx context.Context, pageURL string, opts BrowserbaseOptions) (*PlaywrightResult, error) {
	return renderBrowserbase(ctx, pageURL, opts, false)
}

func renderBrowserbase(ctx context.Context, pageURL string, opts BrowserbaseOptions, discoverMenus bool) (*PlaywrightResult, error) {
	if !isAllowedRendererURL(pageURL) {
		return nil, fmt.Errorf("browserbase render rejected non-public URL %q", pageURL)
	}
	if opts.APIKey == "" {
		return nil, fmt.Errorf("browserbase API key is not configured")
	}
	if opts.RenderWaitMs <= 0 {
		opts.RenderWaitMs = 2000
	}
	if opts.RenderTimeout <= 0 {
		opts.RenderTimeout = 60 * time.Second
	}

	renderCtx, cancel := context.WithTimeout(ctx, opts.RenderTimeout)
	defer cancel()

	session, err := createBrowserbaseSession(renderCtx, opts)
	if err != nil {
		return nil, err
	}
	defer closeBrowserbaseSession(context.Background(), opts.APIKey, session.ID)

	output, err := runPythonJSON(renderCtx, browserbaseScript(discoverMenus, opts.RenderWaitMs), session.ConnectURL, pageURL)
	if err != nil {
		return nil, fmt.Errorf("browserbase render %q failed: %w", pageURL, err)
	}

	var result PlaywrightResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("browserbase output parse failed: %w", err)
	}
	if err := validatePlaywrightResult(pageURL, &result); err != nil {
		return nil, err
	}
	clampPlaywrightResult(&result)
	return &result, nil
}

func browserbaseScript(discoverMenus bool, renderWaitMs int) string {
	if renderWaitMs <= 0 {
		renderWaitMs = 2000
	}
	return fmt.Sprintf(`
import ipaddress, json, os, socket, sys, time
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright

connect_url = sys.argv[1]
target_url = sys.argv[2]
discover_menus = %s
render_wait_ms = %d
discover_triggers_js = %s

def is_public_url(raw_url):
    if os.environ.get("SEO_CRAWLER_ALLOW_PRIVATE_RENDERER_URLS_FOR_TEST") == "1":
        return True
    parsed = urlparse(raw_url)
    if parsed.scheme not in ("http", "https") or not parsed.hostname:
        return False
    try:
        infos = socket.getaddrinfo(parsed.hostname, None)
    except Exception:
        return False
    for info in infos:
        try:
            ip = ipaddress.ip_address(info[4][0])
        except Exception:
            return False
        if ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_multicast or ip.is_reserved or ip.is_unspecified:
            return False
    return True

def guard_route(route):
    if is_public_url(route.request.url):
        route.continue_()
    else:
        route.abort()

def get_page(browser):
    context = browser.contexts[0] if browser.contexts else browser.new_context(viewport={"width": 1440, "height": 900})
    page = context.pages[0] if context.pages else context.new_page()
    page.set_viewport_size({"width": 1440, "height": 900})
    context.route("**/*", guard_route)
    return page

def click_discovered_triggers(page):
    try:
        triggers = page.evaluate(discover_triggers_js)
    except Exception:
        return
    for selector in triggers[:30]:
        try:
            page.click(selector, timeout=1000)
            page.wait_for_timeout(500)
        except Exception:
            pass

def full_scroll(page):
    start = time.time()
    prev_height = 0
    for _ in range(60):
        if time.time() - start > 15:
            break
        page.evaluate("window.scrollBy(0, 800)")
        page.wait_for_timeout(200)
        curr_height = page.evaluate("document.body.scrollHeight")
        if curr_height == prev_height:
            break
        if curr_height > 20000:
            break
        prev_height = curr_height
    page.evaluate("window.scrollTo(0, 0)")
    page.wait_for_timeout(500)

result = {"html": "", "links": [], "images": [], "finalUrl": ""}

with sync_playwright() as p:
    browser = p.chromium.connect_over_cdp(connect_url)
    page = get_page(browser)
    page.goto(target_url, wait_until="domcontentloaded", timeout=30000)
    page.wait_for_timeout(render_wait_ms)
    result["finalUrl"] = page.url
    if not is_public_url(page.url):
        raise RuntimeError(f"Browserbase final URL rejected as non-public: {page.url}")

    if discover_menus:
        click_discovered_triggers(page)
        page.set_viewport_size({"width": 390, "height": 844})
        page.wait_for_timeout(500)
        click_discovered_triggers(page)
        page.set_viewport_size({"width": 1440, "height": 900})
        page.wait_for_timeout(300)
    else:
        full_scroll(page)

    result["finalUrl"] = page.url
    result["html"] = page.content()
    result["links"] = page.evaluate("() => Array.from(document.querySelectorAll('a[href]')).map(a => a.href)")
    result["images"] = page.evaluate("""() => Array.from(document.images).map(img => {
        const rect = img.getBoundingClientRect();
        return {
            src: img.src || '',
            currentSrc: img.currentSrc || img.src || '',
            naturalWidth: img.naturalWidth || 0,
            naturalHeight: img.naturalHeight || 0,
            renderedWidth: Math.round(rect.width || 0),
            renderedHeight: Math.round(rect.height || 0)
        };
    })""")
    browser.close()

print(json.dumps(result))
`, pythonBool(discoverMenus), renderWaitMs, strconv.Quote(discoverTriggersJS))
}

func pythonBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

type browserbaseSession struct {
	ID         string `json:"id"`
	ConnectURL string `json:"connectUrl"`
}

func createBrowserbaseSession(ctx context.Context, opts BrowserbaseOptions) (*browserbaseSession, error) {
	timeoutSeconds := int(opts.RenderTimeout.Seconds()) + 15
	if timeoutSeconds < 60 {
		timeoutSeconds = 60
	}
	body := map[string]any{
		"browserSettings": map[string]any{
			"viewport":      map[string]any{"width": 1440, "height": 900},
			"blockAds":      true,
			"recordSession": false,
			"logSession":    false,
		},
		"timeout": timeoutSeconds,
	}
	if opts.ProjectID != "" {
		body["projectId"] = opts.ProjectID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, browserbaseAPIURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-bb-api-key", opts.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("creating browserbase session: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("creating browserbase session: status %d: %s", resp.StatusCode, string(data))
	}
	var session browserbaseSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parsing browserbase session: %w", err)
	}
	if session.ID == "" || session.ConnectURL == "" {
		return nil, fmt.Errorf("browserbase session response missing id or connectUrl")
	}
	return &session, nil
}

func closeBrowserbaseSession(ctx context.Context, apiKey string, sessionID string) {
	if apiKey == "" || sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, browserbaseAPIURL+"/"+sessionID, nil)
	if err != nil {
		return
	}
	req.Header.Set("x-bb-api-key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}
