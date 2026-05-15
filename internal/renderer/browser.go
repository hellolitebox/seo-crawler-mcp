// Package renderer provides headless browser rendering for JS-heavy pages.
package renderer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// maxRenderedHTML is the hard cap on extracted HTML size (10 MB).
const maxRenderedHTML = 10 * 1024 * 1024

// Pool manages a semaphore-gated set of headless browser renders.
type Pool struct {
	maxSlots      int
	renderWaitMs  int
	renderTimeout time.Duration
	slots         chan struct{}

	// poolCtx / poolCancel allow Close() to cancel all in-flight renders.
	poolCtx    context.Context
	poolCancel context.CancelFunc

	mu     sync.Mutex
	closed bool

	// TODO: Implement render counting + age-based restart policy
	// (spec: restart browser after 100 renders or 60 min).
}

// RenderResult holds the outcome of a single browser render.
type RenderResult struct {
	HTML       string
	FinalURL   string
	RenderTime time.Duration
}

// Options configures the renderer pool.
type Options struct {
	MaxSlots      int
	RenderWaitMs  int
	RenderTimeout time.Duration
}

// NewPool creates a renderer pool with the given options, applying defaults
// for any zero-value fields.
func NewPool(opts Options) *Pool {
	if opts.MaxSlots <= 0 {
		opts.MaxSlots = 2
	}
	if opts.RenderWaitMs <= 0 {
		opts.RenderWaitMs = 2000
	}
	if opts.RenderTimeout <= 0 {
		opts.RenderTimeout = 30 * time.Second
	}

	slots := make(chan struct{}, opts.MaxSlots)
	for i := 0; i < opts.MaxSlots; i++ {
		slots <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Pool{
		maxSlots:      opts.MaxSlots,
		renderWaitMs:  opts.RenderWaitMs,
		renderTimeout: opts.RenderTimeout,
		slots:         slots,
		poolCtx:       ctx,
		poolCancel:    cancel,
	}
}

// Render navigates to rawURL in a headless browser, waits for JS execution,
// and returns the rendered HTML.
func (p *Pool) Render(ctx context.Context, rawURL string) (*RenderResult, error) {
	// Check pool not closed.
	select {
	case <-p.poolCtx.Done():
		return nil, fmt.Errorf("renderer pool is closed")
	default:
	}

	// Acquire slot (respects both caller ctx and pool ctx).
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.poolCtx.Done():
		return nil, fmt.Errorf("renderer pool is closed")
	case <-p.slots:
		// acquired
	}
	defer func() { p.slots <- struct{}{} }()

	// Apply render timeout, merging with pool context so Close() cancels in-flight renders.
	renderCtx, renderCancel := context.WithTimeout(p.poolCtx, p.renderTimeout)
	defer renderCancel()

	// Also respect the caller's context.
	go func() {
		select {
		case <-ctx.Done():
			renderCancel()
		case <-renderCtx.Done():
		}
	}()

	start := time.Now()

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1280, 800),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("lang", "en-US"),
		chromedp.Flag("disable-features", "ServiceWorker"),
		chromedp.Flag("incognito", true),
		chromedp.UserAgent("seo-crawler-mcp/0.1"),
		// Set TZ and LANG env vars on the Chrome process for deterministic rendering.
		chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
			cmd.Env = append(os.Environ(), "TZ=UTC", "LANG=en-US")
		}),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(renderCtx, allocOpts...)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	var html string
	var finalURL string

	// Rendering strategy: Navigate, wait for DOM ready (body element exists),
	// then sleep for a deterministic settle period (renderWaitMs).
	// We intentionally avoid true network-idle detection because it can hang
	// indefinitely on pages with long-polling, WebSockets, or analytics beacons.
	// WaitReady("body") + Sleep is deterministic and used by most production crawlers.
	err := chromedp.Run(taskCtx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(time.Duration(p.renderWaitMs)*time.Millisecond),
		chromedp.OuterHTML("html", &html),
		chromedp.Location(&finalURL),
	)

	elapsed := time.Since(start)

	if err != nil {
		return &RenderResult{
			RenderTime: elapsed,
		}, fmt.Errorf("render %q failed: %w", rawURL, err)
	}

	// Enforce HTML size limit to prevent OOM on pathological pages.
	if len(html) > maxRenderedHTML {
		html = html[:maxRenderedHTML]
	}

	return &RenderResult{
		HTML:       html,
		FinalURL:   finalURL,
		RenderTime: elapsed,
	}, nil
}

// RenderOptions configures optional behaviour for a single render pass.
type RenderOptions struct {
	DiscoverMenus bool // Click nav triggers to find hidden links.
}

// RenderWithOptions is like Render but accepts options that control post-load
// interactions (e.g. clicking navigation triggers to reveal hidden links).
func (p *Pool) RenderWithOptions(ctx context.Context, rawURL string, opts RenderOptions) (*RenderResult, error) {
	if !opts.DiscoverMenus {
		return p.Render(ctx, rawURL)
	}

	// ── acquire slot ────────────────────────────────────────────────────
	select {
	case <-p.poolCtx.Done():
		return nil, fmt.Errorf("renderer pool is closed")
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.poolCtx.Done():
		return nil, fmt.Errorf("renderer pool is closed")
	case <-p.slots:
	}
	defer func() { p.slots <- struct{}{} }()

	renderCtx, renderCancel := context.WithTimeout(p.poolCtx, p.renderTimeout)
	defer renderCancel()

	go func() {
		select {
		case <-ctx.Done():
			renderCancel()
		case <-renderCtx.Done():
		}
	}()

	start := time.Now()

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1280, 800),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("lang", "en-US"),
		chromedp.Flag("disable-features", "ServiceWorker"),
		chromedp.Flag("incognito", true),
		chromedp.UserAgent("seo-crawler-mcp/0.1"),
		chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
			cmd.Env = append(os.Environ(), "TZ=UTC", "LANG=en-US")
		}),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(renderCtx, allocOpts...)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	// ── 1. Normal page load ─────────────────────────────────────────────
	var finalURL string
	err := chromedp.Run(taskCtx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(time.Duration(p.renderWaitMs)*time.Millisecond),
		chromedp.Location(&finalURL),
	)
	if err != nil {
		return &RenderResult{RenderTime: time.Since(start)},
			fmt.Errorf("render %q failed: %w", rawURL, err)
	}

	// ── 2. Desktop viewport: discover & click nav triggers ──────────────
	clickDiscoveredTriggers(taskCtx)

	// ── 3. Mobile viewport: discover & click triggers that only appear
	//       at narrow widths (hamburger menus, etc.) ─────────────────────
	_ = chromedp.Run(taskCtx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Sleep(500*time.Millisecond),
	)
	clickDiscoveredTriggers(taskCtx)

	// Restore desktop viewport so the final HTML reflects all expanded menus.
	_ = chromedp.Run(taskCtx,
		chromedp.EmulateViewport(1280, 800),
		chromedp.Sleep(300*time.Millisecond),
	)

	// ── 4. Extract fully-expanded HTML ──────────────────────────────────
	var html string
	err = chromedp.Run(taskCtx,
		chromedp.OuterHTML("html", &html),
	)
	elapsed := time.Since(start)
	if err != nil {
		return &RenderResult{RenderTime: elapsed},
			fmt.Errorf("render %q: HTML extraction failed: %w", rawURL, err)
	}

	if len(html) > maxRenderedHTML {
		html = html[:maxRenderedHTML]
	}

	return &RenderResult{
		HTML:       html,
		FinalURL:   finalURL,
		RenderTime: elapsed,
	}, nil
}

// discoverTriggersJS is the JavaScript that finds visible navigation trigger
// elements and returns their unique CSS selectors.
const discoverTriggersJS = `
(function() {
    const seen = new Set();
    const result = [];

    function uniqueSelector(el) {
        if (el.id) return '#' + CSS.escape(el.id);
        const path = [];
        let cur = el;
        while (cur && cur !== document.body && cur !== document.documentElement) {
            let selector = cur.tagName.toLowerCase();
            if (cur.id) { path.unshift('#' + CSS.escape(cur.id)); break; }
            const parent = cur.parentNode;
            if (parent) {
                const siblings = [...parent.children];
                if (siblings.length > 1) {
                    const index = siblings.indexOf(cur) + 1;
                    selector += ':nth-child(' + index + ')';
                }
            }
            path.unshift(selector);
            cur = cur.parentNode;
        }
        return path.join(' > ');
    }

    function add(el) {
        // Must be visible (offsetParent is null for display:none / hidden
        // ancestors, but fixed/sticky elements also have null offsetParent).
        if (el.offsetParent === null && getComputedStyle(el).position !== 'fixed'
            && getComputedStyle(el).position !== 'sticky') return;
        const sel = uniqueSelector(el);
        if (!seen.has(sel)) { seen.add(sel); result.push(sel); }
    }

    // Hamburger / mobile-nav buttons
    document.querySelectorAll(
        'button[aria-label*="menu" i], button[aria-label*="nav" i], ' +
        'button.hamburger, button.menu-toggle, .navbar-toggler, ' +
        '[data-toggle="collapse"], [data-bs-toggle="collapse"]'
    ).forEach(add);

    // Dropdown triggers
    document.querySelectorAll(
        '[aria-haspopup], [data-dropdown], .dropdown-toggle, ' +
        'nav button, nav [role="button"], nav details > summary'
    ).forEach(add);

    // Accordion / expandable triggers
    document.querySelectorAll(
        '[aria-expanded="false"], details:not([open]) > summary'
    ).forEach(add);

    // Text-based nav triggers: elements in header/nav whose text matches
    // common menu labels that often hide sub-navigation behind a click.
    const menuLabels = /^(services|products|solutions|resources|company|menu|more|explore)$/i;
    document.querySelectorAll('header *, nav *, [role="navigation"] *').forEach(el => {
        const text = el.textContent.trim();
        if (menuLabels.test(text) && (el.tagName === 'BUTTON' || el.tagName === 'A' ||
            el.tagName === 'SPAN' || el.tagName === 'DIV' || el.tagName === 'LI') &&
            !el.href) {
            add(el);
        }
    });

    // Buttons with text "Menu" (common mobile hamburger label)
    document.querySelectorAll('button').forEach(el => {
        if (/^menu$/i.test(el.textContent.trim())) add(el);
    });

    return result;
})()
`

// clickDiscoveredTriggers runs the JS trigger discovery and clicks each one.
func clickDiscoveredTriggers(taskCtx context.Context) {
	var triggers []string
	if err := chromedp.Run(taskCtx,
		chromedp.Evaluate(discoverTriggersJS, &triggers),
	); err != nil || len(triggers) == 0 {
		return
	}

	// Cap to avoid pathological pages with hundreds of accordion items.
	const maxTriggers = 30
	if len(triggers) > maxTriggers {
		triggers = triggers[:maxTriggers]
	}

	for _, sel := range triggers {
		// Best-effort: ignore click errors (element may have disappeared,
		// be covered by an overlay, etc.).
		_ = chromedp.Run(taskCtx,
			chromedp.Click(sel, chromedp.ByQuery),
			chromedp.Sleep(500*time.Millisecond),
		)
	}
}

// Close marks the pool as closed and cancels all in-flight renders.
func (p *Pool) Close() {
	p.poolCancel() // cancels all in-flight renders
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}
