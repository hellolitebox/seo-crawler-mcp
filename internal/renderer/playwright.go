package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// PlaywrightResult holds the output from the Playwright menu discovery script.
type PlaywrightResult struct {
	HTML  string   `json:"html"`
	Links []string `json:"links"`
}

// playwrightAvailable caches the result of the availability check.
var (
	playwrightOnce      sync.Once
	playwrightAvailable bool
)

// IsPlaywrightAvailable returns true if python3 and the playwright package
// are installed. The result is cached after the first call.
func IsPlaywrightAvailable() bool {
	playwrightOnce.Do(func() {
		if _, err := exec.LookPath("python3"); err != nil {
			return
		}
		cmd := exec.Command("python3", "-c", "import playwright; print('ok')")
		output, err := cmd.Output()
		playwrightAvailable = err == nil && strings.TrimSpace(string(output)) == "ok"
	})
	return playwrightAvailable
}

// RenderWithPlaywright uses Playwright (via Python subprocess) to render a page
// with full menu discovery (desktop + mobile viewports, clicking nav triggers).
// Returns the final HTML and all discovered link URLs.
func RenderWithPlaywright(ctx context.Context, pageURL string) (*PlaywrightResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", "-c", playwrightScript(), pageURL)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("playwright render failed: %w; stderr: %s", err, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("playwright render failed: %w", err)
	}

	var result PlaywrightResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("playwright output parse failed: %w", err)
	}

	return &result, nil
}

// RenderPageContentOnly uses Playwright to render a page with full scroll
// but WITHOUT menu discovery clicks. This preserves the page's own content
// for accurate word count and image extraction.
func RenderPageContentOnly(ctx context.Context, pageURL string) (*PlaywrightResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	script := `
import os, sys, json
from playwright.sync_api import sync_playwright
import time as _time

url = sys.argv[1]
result = {"html": "", "links": []}

_chromium_path = os.environ.get("CHROMIUM_PATH") or None

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=_chromium_path)
    page = browser.new_page(viewport={"width": 1440, "height": 900})
    page.goto(url, wait_until="networkidle", timeout=30000)
    page.wait_for_timeout(2000)

    # Full scroll to trigger lazy loading
    _scroll_start = _time.time()
    prev_height = 0
    for _ in range(60):
        if _time.time() - _scroll_start > 15:
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

    result["html"] = page.content()
    result["links"] = page.evaluate("() => [...document.querySelectorAll('a[href]')].map(a => a.href)")
    browser.close()

print(json.dumps(result))
`

	cmd := exec.CommandContext(ctx, "python3", "-c", script, pageURL)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("playwright content render failed: %w; stderr: %s", err, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("playwright content render failed: %w", err)
	}

	var result PlaywrightResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("playwright content output parse failed: %w", err)
	}

	return &result, nil
}

func playwrightScript() string {
	return `
import os, sys, json
from playwright.sync_api import sync_playwright

url = sys.argv[1]

_chromium_path = os.environ.get("CHROMIUM_PATH") or None

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=_chromium_path)

    result = {"html": "", "links": []}

    # Desktop viewport
    page = browser.new_page(viewport={"width": 1440, "height": 900})
    page.goto(url, wait_until="networkidle", timeout=30000)
    page.wait_for_timeout(2000)

    # Collect links incrementally after each interaction
    all_found = set(page.evaluate("""
        () => [...document.querySelectorAll('a[href]')].map(a => a.href)
    """))

    # Phase 1: Click known menu labels (covers ~80% of SaaS/B2B navs)
    menu_labels = ["services", "products", "solutions", "resources", "company",
                   "more", "explore", "platform", "developers", "pricing",
                   "features", "about", "industries", "use cases", "integrations"]

    for label in menu_labels:
        try:
            for variant in [label.capitalize(), label.upper(), label.lower(), label.title()]:
                try:
                    el = page.locator(f"text={variant}").first
                    if el.is_visible(timeout=200):
                        el.click()
                        page.wait_for_timeout(400)
                        new_links = page.evaluate("() => [...document.querySelectorAll('a[href]')].map(a => a.href)")
                        all_found.update(new_links)
                        break
                except:
                    continue
        except:
            pass

    # Phase 2: Click ARIA-based triggers
    triggers = page.locator("button[aria-haspopup], [aria-expanded='false'], nav button, header button")
    count = triggers.count()
    for i in range(min(count, 20)):
        try:
            t = triggers.nth(i)
            if t.is_visible(timeout=150):
                t.click()
                page.wait_for_timeout(300)
                new_links = page.evaluate("() => [...document.querySelectorAll('a[href]')].map(a => a.href)")
                all_found.update(new_links)
        except:
            pass

    # Phase 3: Heuristic — click non-link elements in nav/header that look like dropdown triggers
    # Only targets direct children of nav/header (not deeply nested), caps at 10 clicks
    page.evaluate("""() => {
        let clicks = 0;
        const maxClicks = 10;
        const clicked = new Set();
        
        // Only check direct children and grandchildren of header/nav (not all descendants)
        const containers = document.querySelectorAll('header > *, nav > *, [role="navigation"] > *, header > * > *, nav > * > *');
        
        for (const el of containers) {
            if (clicks >= maxClicks) break;
            
            // Only click non-link elements that look interactive
            if (el.tagName === 'A' || el.tagName === 'IMG' || el.tagName === 'SVG' ||
                el.tagName === 'PATH' || el.tagName === 'STYLE' || el.tagName === 'SCRIPT' ||
                el.tagName === 'UL' || el.tagName === 'OL' || el.tagName === 'LI') continue;
            
            const rect = el.getBoundingClientRect();
            if (rect.height <= 0 || rect.height > 60 || rect.width <= 10 || rect.width > 250) continue;
            if (el.offsetParent === null && getComputedStyle(el).position === 'static') continue;
            
            // Must have short text (nav items are short)
            const text = el.textContent.trim();
            if (text.length === 0 || text.length > 30) continue;
            
            // Skip if it's a link wrapper (has <a> children)
            if (el.querySelector('a[href]')) continue;
            
            const id = text.toLowerCase();
            if (clicked.has(id)) continue;
            clicked.add(id);
            
            try { el.click(); clicks++; } catch(e) {}
        }
    }""")
    page.wait_for_timeout(500)
    new_links = page.evaluate("() => [...document.querySelectorAll('a[href]')].map(a => a.href)")
    all_found.update(new_links)

    # Mobile viewport: all via JS to avoid slow Playwright locators
    page.set_viewport_size({"width": 390, "height": 844})
    page.wait_for_timeout(300)

    # Single JS call: click hamburger, wait, click sub-menus, collect all links
    mobile_links = page.evaluate("""() => {
        // Step 1: Find and click hamburger
        for (const b of document.querySelectorAll('button')) {
            const t = b.textContent.trim().toLowerCase();
            const al = (b.getAttribute('aria-label') || '').toLowerCase();
            if (t === 'menu' || al.includes('menu') || al.includes('nav')) {
                b.click();
                break;
            }
        }
        return [...document.querySelectorAll('a[href]')].map(a => a.href);
    }""")
    all_found.update(mobile_links)
    page.wait_for_timeout(300)

    # Click sub-menu triggers
    page.evaluate("""() => {
        const labels = ['services','products','solutions','resources','company','features','platform','developers'];
        for (const el of document.querySelectorAll('header *, nav *, [role=navigation] *')) {
            const t = el.textContent.trim().toLowerCase();
            if (labels.includes(t) && el.tagName !== 'A') { el.click(); }
        }
    }""")
    page.wait_for_timeout(300)
    all_found.update(page.evaluate("() => [...document.querySelectorAll('a[href]')].map(a => a.href)"))

    # Collect any remaining links
    final_links = page.evaluate("() => [...document.querySelectorAll('a[href]')].map(a => a.href)")
    all_found.update(final_links)

    # Scroll the full page to trigger lazy-loaded content (intersection observers)
    # Caps: max 20,000px height, max 15 seconds, stop if height stabilizes
    import time as _time
    _scroll_start = _time.time()
    _max_scroll_height = 20000
    _max_scroll_seconds = 15
    prev_height = 0
    for _ in range(60):
        if _time.time() - _scroll_start > _max_scroll_seconds:
            break
        page.evaluate("window.scrollBy(0, 800)")
        page.wait_for_timeout(200)
        curr_height = page.evaluate("document.body.scrollHeight")
        if curr_height == prev_height:
            break
        if curr_height > _max_scroll_height:
            break
        prev_height = curr_height
    page.evaluate("window.scrollTo(0, 0)")
    page.wait_for_timeout(500)

    # Collect any links that appeared after scroll
    all_found.update(page.evaluate("() => [...document.querySelectorAll('a[href]')].map(a => a.href)"))

    # Get final HTML
    result["html"] = page.content()
    result["links"] = list(all_found)

    browser.close()

print(json.dumps(result))
`
}
