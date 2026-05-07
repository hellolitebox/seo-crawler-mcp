// Package engine orchestrates the SEO crawl pipeline.
package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"net/http"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/crawl"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/frontier"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/issues"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/lighthouse"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/materialize"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/renderer"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/robots"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/textquality"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

// EngineConfig holds all dependencies for the crawl engine.
type EngineConfig struct {
	DB           *storage.DB
	Fetcher      *fetcher.Fetcher
	RateLimiter  *fetcher.RateLimiter
	ScopeChecker *urlutil.ScopeChecker
	SSRFGuard    *ssrf.Guard
	Config       *config.Config
	Renderer     *renderer.Pool
}

// Engine orchestrates a complete crawl pipeline.
type Engine struct {
	db           *storage.DB
	fetcher      *fetcher.Fetcher
	rateLimiter  *fetcher.RateLimiter
	scopeChecker *urlutil.ScopeChecker
	ssrfGuard    *ssrf.Guard
	config       *config.Config
	renderer     *renderer.Pool

	// robotsRules caches parsed robots.txt per host during a crawl.
	robotsRules   map[string]*robots.RobotsFile
	robotsRulesMu sync.RWMutex
}

// New creates a new crawl engine.
func New(cfg EngineConfig) *Engine {
	return &Engine{
		db:           cfg.DB,
		fetcher:      cfg.Fetcher,
		rateLimiter:  cfg.RateLimiter,
		scopeChecker: cfg.ScopeChecker,
		ssrfGuard:    cfg.SSRFGuard,
		config:       cfg.Config,
		renderer:     cfg.Renderer,
	}
}

type fetchResult struct {
	urlID    int64
	url      string
	host     string
	depth    int
	fetchSeq int
	result   *fetcher.FetchResult
	err      error
}

// discoveredImage holds a resolved image URL and its source page URL ID.
type discoveredImage struct {
	normalizedURL string
	host          string
	isInternal    bool
	sourceURLID   int64
}

// discoveredAsset holds a resolved asset URL, its source page, and reference type.
type discoveredAsset struct {
	normalizedURL string
	host          string
	isInternal    bool
	sourceURLID   int64
	refType       string // "script_src", "stylesheet_href", "font_preload", "icon_href", "video_src", "audio_src", "preload_href"
}

type parseResult struct {
	fetchResult
	page   *parser.ParseResult
	edges  []crawl.DiscoveredEdge
	issues []issues.DetectedIssue
	images []discoveredImage
	assets []discoveredAsset
}

type persistItem struct {
	parseResult
	fetchSeq int
}

func recoverWorker(cancel context.CancelCauseFunc, name string) {
	if r := recover(); r != nil {
		cancel(fmt.Errorf("%s panic: %v", name, r))
	}
}

func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// emitPhase records a phase transition event so the live UI can show what the
// engine is currently doing (post-crawl phases otherwise look idle).
func (e *Engine) emitPhase(jobID, phase, message string) {
	if e.db == nil {
		return
	}
	payload, err := json.Marshal(struct {
		Phase   string `json:"phase"`
		Message string `json:"message"`
	}{phase, message})
	if err != nil {
		log.Printf("engine: emitPhase marshal failed: %v", err)
		return
	}
	details := string(payload)
	if _, err := e.db.InsertEvent(jobID, "phase", &details, nil); err != nil {
		log.Printf("engine: emitPhase insert failed: %v", err)
	}
	log.Printf("engine: phase=%s %s", phase, message)
}

// RunCrawl executes a full crawl job. Blocks until complete or cancelled.
func (e *Engine) RunCrawl(ctx context.Context, jobID string) error {
	// 1. Init: load job, update status
	job, err := e.db.GetJob(jobID)
	if err != nil {
		return fmt.Errorf("loading job: %w", err)
	}
	if job.Status != "queued" {
		return fmt.Errorf("job %s has status %q, expected queued", jobID, job.Status)
	}
	if err := e.db.UpdateJobStarted(jobID); err != nil {
		return fmt.Errorf("starting job: %w", err)
	}

	// 2. Purge expired analyze jobs
	e.db.PurgeExpiredAnalyzeJobs()

	// 3. Seed: parse seed URLs, normalize, upsert, push to frontier
	var seeds []string
	if err := json.Unmarshal([]byte(job.SeedURLs), &seeds); err != nil {
		return e.failJob(jobID, fmt.Errorf("parsing seed URLs: %w", err))
	}

	q := frontier.New()

	// Track query variant counts per path for crawl trap detection
	var queryVariantsMu sync.Mutex
	queryVariants := map[string]int{}

	// Track pages crawled for MaxPages limit
	var pagesCrawled atomic.Int64

	for _, seedURL := range seeds {
		normalized, err := urlutil.Normalize(seedURL)
		if err != nil {
			log.Printf("engine: skipping invalid seed URL %q: %v", seedURL, err)
			continue
		}
		parsed, err := url.Parse(normalized)
		if err != nil {
			continue
		}
		host := parsed.Hostname()
		urlID, err := e.db.UpsertURL(jobID, normalized, host, "queued", true, "seed")
		if err != nil {
			return e.failJob(jobID, fmt.Errorf("upserting seed URL: %w", err))
		}
		q.Push(frontier.Item{
			URLID:         urlID,
			NormalizedURL: normalized,
			Host:          host,
			Depth:         0,
		})
	}

	if q.Len() == 0 {
		return e.failJob(jobID, fmt.Errorf("no valid seed URLs"))
	}

	// Create scope checker from first seed if not provided via config.
	if e.scopeChecker == nil {
		first := q.Peek()
		scopeMode := "registrable_domain"
		var allowedHosts []string
		if e.config != nil {
			scopeMode = string(e.config.ScopeMode)
			allowedHosts = e.config.AllowedHosts
		}
		// Try to parse from job config_json as well.
		var jobCfg struct {
			ScopeMode    string   `json:"scopeMode"`
			AllowedHosts []string `json:"allowedHosts"`
		}
		if err := json.Unmarshal([]byte(job.ConfigJSON), &jobCfg); err == nil {
			if jobCfg.ScopeMode != "" {
				scopeMode = jobCfg.ScopeMode
			}
			if len(jobCfg.AllowedHosts) > 0 {
				allowedHosts = jobCfg.AllowedHosts
			}
		}
		sc, err := urlutil.NewScopeChecker(scopeMode, first.Host, allowedHosts)
		if err != nil {
			return e.failJob(jobID, fmt.Errorf("creating scope checker: %w", err))
		}
		e.scopeChecker = sc
	}

	// ---- Host onboarding: robots.txt + sitemap discovery ----
	e.robotsRules = map[string]*robots.RobotsFile{}
	{
		userAgent := e.config.UserAgent
		if userAgent == "" {
			userAgent = "seo-crawler-mcp/0.1"
		}
		sitemapMax := e.config.MaxSitemapEntries
		if sitemapMax <= 0 {
			sitemapMax = 500000
		}
		robotsPolicy := string(e.config.RobotsUnreachablePolicy)
		if robotsPolicy == "" {
			robotsPolicy = "allow"
		}
		onboarder := crawl.NewHostOnboarderWithPolicy(e.fetcher, e.db, sitemapMax, userAgent, robotsPolicy)

		seenHosts := map[string]bool{}
		for _, seedURL := range seeds {
			parsed, parseErr := url.Parse(seedURL)
			if parseErr != nil {
				continue
			}
			// Use parsed.Host (includes port) for URL construction in onboarding.
			hostWithPort := parsed.Host
			hostOnly := parsed.Hostname()
			if seenHosts[hostWithPort] {
				continue
			}
			seenHosts[hostWithPort] = true

			info, onboardErr := onboarder.OnboardHost(ctx, jobID, hostWithPort, parsed.Scheme)
			if onboardErr != nil {
				log.Printf("engine: onboarding host %q: %v", hostWithPort, onboardErr)
				continue
			}

			// Cache robots rules for fetch-time checking (keyed by hostname without port)
			if info.RobotsFile != nil {
				e.robotsRulesMu.Lock()
				e.robotsRules[hostOnly] = info.RobotsFile
				e.robotsRulesMu.Unlock()
			}

			// Apply crawl delay to rate limiter (keyed by hostname as used in fetcher)
			if info.CrawlDelay > 0 {
				e.rateLimiter.SetCrawlDelay(hostOnly, info.CrawlDelay)
				log.Printf("engine: crawl-delay for %q set to %v", hostOnly, info.CrawlDelay)
			}

			// Add sitemap URLs to frontier
			for _, entry := range info.SitemapEntries {
				normalized, normErr := urlutil.Normalize(entry.Loc)
				if normErr != nil {
					continue
				}
				parsedURL, parseErr2 := url.Parse(normalized)
				if parseErr2 != nil {
					continue
				}
				entryHost := parsedURL.Hostname()

				// Check scope
				if e.scopeChecker != nil && !e.scopeChecker.IsInScope(normalized) {
					continue
				}

				// Check robots rules before adding
				if e.config.RespectRobots && info.RobotsFile != nil {
					if !info.RobotsFile.IsAllowed(userAgent, parsedURL.Path) {
						continue
					}
				}

				urlID, upsertErr := e.db.UpsertURL(jobID, normalized, entryHost, "queued", true, "sitemap")
				if upsertErr != nil {
					continue
				}

				if !q.Contains(urlID) {
					q.Push(frontier.Item{
						URLID:         urlID,
						NormalizedURL: normalized,
						Host:          entryHost,
						Depth:         1, // sitemap URLs are depth 1
					})
				}
			}

			// Log onboarding event
			eventDetails := fmt.Sprintf(`{"host":%q,"sitemapEntries":%d,"crawlDelay":%q}`,
				hostWithPort, len(info.SitemapEntries), info.CrawlDelay.String())
			e.db.InsertEvent(jobID, "host_onboarded", &eventDetails, nil)
		}
	}

	// Channels
	// fetchQueue feeds items from the dispatcher to fetcher workers.
	fetchQueue := make(chan frontier.Item, 64)
	fetchResults := make(chan fetchResult, 64)
	persistQueue := make(chan persistItem, 128)

	// Monotonic fetch sequence counter
	var fetchSeq atomic.Int64

	// Concurrency
	concurrency := e.config.GlobalConcurrency
	if concurrency < 1 {
		concurrency = 8
	}
	parserCount := 4

	// Use context.WithCancelCause so any fatal error (e.g. persister) can unwind the whole pipeline.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	statusWatchDone := make(chan struct{})
	go func() {
		defer recoverWorker(cancel, "status watcher")
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-statusWatchDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				job, err := e.db.GetJob(jobID)
				if err != nil {
					continue
				}
				if job.Status == "cancelling" {
					cancel(context.Canceled)
					return
				}
			}
		}
	}()
	defer close(statusWatchDone)

	// Counters for job stats
	var urlsDiscovered atomic.Int64
	urlsDiscovered.Store(int64(q.Len()))
	var issuesFound atomic.Int64

	// Periodic counter flush: write in-memory atomics to DB every 2 s so
	// polling clients see live progress instead of all-zeros until completion.
	counterTicker := time.NewTicker(2 * time.Second)
	counterDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-counterDone:
				return
			case <-ctx.Done():
				return
			case <-counterTicker.C:
				if err := e.db.UpdateJobCounters(jobID, int(pagesCrawled.Load()), int(urlsDiscovered.Load()), int(issuesFound.Load())); err != nil {
					log.Printf("engine: counter flush failed: %v", err)
				}
			}
		}
	}()
	defer func() {
		counterTicker.Stop()
		close(counterDone)
	}()

	// inFlight tracks items between dispatch and persist completion.
	var inFlight atomic.Int64

	// ---- Persister (1 goroutine) ----
	var persisterWg sync.WaitGroup
	persisterWg.Add(1)
	go func() {
		defer recoverWorker(cancel, "persister")
		defer persisterWg.Done()
		for item := range persistQueue {
			if pErr := e.persistItem(ctx, jobID, item); pErr != nil {
				var lastErr error
				for attempt := 1; attempt <= 3; attempt++ {
					select {
					case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
					case <-ctx.Done():
						inFlight.Add(-1)
						return
					}
					lastErr = e.persistItem(ctx, jobID, item)
					if lastErr == nil {
						break
					}
				}
				if lastErr != nil {
					inFlight.Add(-1)
					cancel(fmt.Errorf("persist failed after retries: %w", lastErr))
					return
				}
			}
			inFlight.Add(-1)
		}
	}()

	// ---- Parser Pool ----
	var parserWg sync.WaitGroup
	for range parserCount {
		parserWg.Add(1)
		go func() {
			defer recoverWorker(cancel, "parser")
			defer parserWg.Done()
			for fr := range fetchResults {
				pr := e.processParseResult(ctx, jobID, fr, q, &pagesCrawled, &urlsDiscovered, &queryVariantsMu, queryVariants)
				issuesFound.Add(int64(len(pr.issues)))
				select {
				case persistQueue <- persistItem{
					parseResult: pr,
					fetchSeq:    fr.fetchSeq,
				}:
				case <-ctx.Done():
					inFlight.Add(-1)
					return
				}
			}
		}()
	}

	// ---- Fetcher Pool ----
	var fetcherWg sync.WaitGroup
	for range concurrency {
		fetcherWg.Add(1)
		go func() {
			defer recoverWorker(cancel, "fetcher")
			defer fetcherWg.Done()
			for item := range fetchQueue {
				// Check scope
				if !e.scopeChecker.IsInScope(item.NormalizedURL) {
					inFlight.Add(-1)
					continue
				}

				// Check robots.txt rules
				if e.config.RespectRobots {
					parsedItem, parseErr := url.Parse(item.NormalizedURL)
					if parseErr == nil {
						e.robotsRulesMu.RLock()
						rf := e.robotsRules[item.Host]
						e.robotsRulesMu.RUnlock()
						if rf != nil && !rf.IsAllowed(e.config.UserAgent, parsedItem.Path) {
							e.db.UpdateURLStatus(item.URLID, "robots_blocked")
							inFlight.Add(-1)
							continue
						}
					}
				}

				// Acquire rate limiter
				if err := e.rateLimiter.AcquireContext(ctx, item.Host); err != nil {
					inFlight.Add(-1)
					return
				}

				// Get fetch sequence (must be unique per fetch)
				seq := int(fetchSeq.Add(1))

				// Fetch
				result, fetchErr := e.fetcher.FetchContext(ctx, item.NormalizedURL)

				// Release rate limiter
				e.rateLimiter.Release(item.Host)

				// Track TTFB for slow-host detection
				if result != nil {
					if avgTTFB, full := e.rateLimiter.RecordTTFB(item.Host, result.TTFBMS); full && avgTTFB > 5000 {
						detailsJSON := fmt.Sprintf(`{"host":%q,"avgTtfbMs":%d}`, item.Host, avgTTFB)
						e.db.InsertIssue(storage.IssueInput{
							JobID:       jobID,
							IssueType:   "slow_host",
							Severity:    "info",
							Scope:       "page_local",
							DetailsJSON: &detailsJSON,
						})
					}
				}

				fr := fetchResult{
					urlID:    item.URLID,
					url:      item.NormalizedURL,
					host:     item.Host,
					depth:    item.Depth,
					fetchSeq: seq,
					result:   result,
					err:      fetchErr,
				}

				// Update URL status
				if fetchErr != nil {
					e.db.UpdateURLStatus(item.URLID, "errored")
					detailsJSON := fmt.Sprintf(`{"error":%q,"url":%q}`, fetchErr.Error(), item.NormalizedURL)
					e.db.InsertEvent(jobID, "fetch_error", &detailsJSON, &item.NormalizedURL)
				} else {
					e.db.UpdateURLStatus(item.URLID, "fetched")
				}

				select {
				case fetchResults <- fr:
				case <-ctx.Done():
					inFlight.Add(-1)
					return
				}
			}
		}()
	}

	// ---- Dispatcher: pulls from frontier and sends to fetchQueue ----
	// This runs on the main goroutine's ticker loop.
	// inFlight is incremented HERE (before sending to fetchQueue) and
	// decremented in the persister (after persist completes).
	// This eliminates the race between pop and tracking.

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	var completionErr error
loop:
	for {
		// Drain frontier as much as possible
		for {
			item, ok := q.Pop()
			if !ok {
				break
			}
			inFlight.Add(1)
			select {
			case fetchQueue <- item:
			case <-ctx.Done():
				inFlight.Add(-1)
				completionErr = context.Cause(ctx)
				break loop
			}
		}

		// Check completion: nothing in queue and nothing in flight
		if q.Len() == 0 && inFlight.Load() == 0 {
			break loop
		}

		// Check for cancellation (includes persister fatal errors via WithCancelCause)
		select {
		case <-ctx.Done():
			completionErr = context.Cause(ctx)
			break loop
		case <-ticker.C:
			// Continue loop to check frontier again
		}
	}

	// Shutdown pipeline in order
	close(fetchQueue)
	fetcherWg.Wait()

	close(fetchResults)
	parserWg.Wait()

	close(persistQueue)
	persisterWg.Wait()

	// --- Post-crawl: sitemap gap browser escalation (hybrid/browser mode) ---
	if completionErr == nil && e.config.RenderMode != config.RenderModeStatic {
		e.emitPhase(jobID, "sitemap_gap", "checking sitemap URLs missing from crawl (JS render)")
		escalated := e.sitemapGapEscalation(ctx, jobID)
		if escalated > 0 {
			log.Printf("engine: sitemap gap escalation discovered %d new URLs", escalated)
		}
	}

	// --- Post-crawl: browser re-render all pages to capture lazy-loaded content ---
	if completionErr == nil && e.config.RenderMode != config.RenderModeStatic && renderer.IsPlaywrightAvailable() {
		e.browserEnrichPages(ctx, jobID)
	}

	// --- Post-crawl: HEAD-check discovered image assets ---
	if completionErr == nil {
		e.headCheckAssets(ctx, jobID)
	}

	// --- Post-crawl: Performance + Accessibility audits (parallel) ---
	if completionErr == nil {
		var auditWg sync.WaitGroup
		log.Printf("engine: PSI API key configured: %v", e.config != nil && e.config.PSIAPIKey != "")
		if e.config != nil && e.config.PSIAPIKey != "" {
			auditWg.Add(1)
			go func() {
				defer recoverWorker(cancel, "lighthouse audits")
				defer auditWg.Done()
				e.runLighthouseAudits(ctx, jobID)
			}()
		}
		if renderer.IsPlaywrightAvailable() {
			auditWg.Add(1)
			go func() {
				defer recoverWorker(cancel, "axe audits")
				defer auditWg.Done()
				e.runAxeAudits(ctx, jobID)
			}()
		}
		auditWg.Wait()
	}

	// --- Post-crawl: markdown content negotiation check ---
	if completionErr == nil {
		e.emitPhase(jobID, "markdown_negotiation", "checking which pages support text/markdown")
		e.checkMarkdownNegotiation(ctx, jobID)
	}

	// --- Post-crawl: text quality checks via LanguageTool ---
	if completionErr == nil && e.config.LanguageToolURL != "" {
		e.runTextQualityChecks(ctx, jobID)
	}

	// --- Post-crawl: recalculate inbound/outbound edge counts and true shortest-path depths on pages ---
	if completionErr == nil {
		e.db.Exec(`
			UPDATE pages SET inbound_edge_count = (
				SELECT COUNT(*) FROM edges e
				WHERE e.job_id = pages.job_id
				  AND e.declared_target_url = (SELECT normalized_url FROM urls WHERE id = pages.url_id AND job_id = pages.job_id)
				  AND e.is_internal = 1 AND e.relation_type = 'link'
			) WHERE job_id = ?`, jobID)
		e.db.Exec(`
			UPDATE pages SET outbound_edge_count = (
				SELECT COUNT(*) FROM edges e
				WHERE e.job_id = pages.job_id
				  AND e.source_url_id = pages.url_id
				  AND e.is_internal = 1 AND e.relation_type = 'link'
			) WHERE job_id = ?`, jobID)
		if depthErr := e.recomputePageDepths(jobID); depthErr != nil {
			log.Printf("engine: page depth recomputation failed: %v", depthErr)
		}
		log.Printf("engine: recalculated edge counts for job %s", jobID)
	}

	// --- Post-crawl: global issue detection + materialization ---
	if completionErr == nil {
		e.emitPhase(jobID, "global_issues", "detecting site-wide issues (duplicates, clusters, gaps)")
		globalCfg := issues.DefaultGlobalConfig()
		globalCount, globalErr := issues.DetectGlobalIssues(e.db, jobID, globalCfg)
		if globalErr != nil {
			log.Printf("engine: global issue detection failed: %v", globalErr)
		} else {
			issuesFound.Add(int64(globalCount))
			e.emitPhase(jobID, "global_issues_done", fmt.Sprintf("%d global issues detected", globalCount))
		}

		// Materialize canonical clusters, duplicate clusters, URL groups
		e.emitPhase(jobID, "materializing", "writing report rollups")
		if matErr := materialize.Materialize(e.db, jobID); matErr != nil {
			log.Printf("engine: materialization failed: %v", matErr)
		}

		// Re-sync issuesFound from the actual issues table so the job row
		// reflects everything written by post-crawl phases (text quality, audits, etc.)
		var actual int
		if scanErr := e.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE job_id = ?`, jobID).Scan(&actual); scanErr == nil {
			if int64(actual) > issuesFound.Load() {
				issuesFound.Store(int64(actual))
			}
		}
	}

	// Update final counters
	e.db.UpdateJobCounters(jobID, int(pagesCrawled.Load()), int(urlsDiscovered.Load()), int(issuesFound.Load()))

	// Set final status
	if completionErr != nil {
		if isContextCancellation(completionErr) || isContextCancellation(context.Cause(ctx)) {
			msg := completionErr.Error()
			e.db.UpdateJobFinished(jobID, "cancelled", &msg)
			return context.Canceled
		}
		errMsg := completionErr.Error()
		e.db.UpdateJobFinished(jobID, "failed", &errMsg)
		return completionErr
	}

	e.db.UpdateJobFinished(jobID, "completed", nil)
	return nil
}

// processParseResult handles parsing, edge building, issue detection, and frontier expansion.
func (e *Engine) processParseResult(
	ctx context.Context,
	jobID string,
	fr fetchResult,
	q *frontier.Queue,
	pagesCrawled *atomic.Int64,
	urlsDiscovered *atomic.Int64,
	queryVariantsMu *sync.Mutex,
	queryVariants map[string]int,
) parseResult {
	pr := parseResult{
		fetchResult: fr,
		edges:       []crawl.DiscoveredEdge{},
		issues:      []issues.DetectedIssue{},
	}

	if fr.err != nil || fr.result == nil {
		return pr
	}

	// Rate limited detection
	if fr.result.StatusCode == 429 {
		pr.issues = append(pr.issues, issues.DetectedIssue{
			IssueType:   "rate_limited",
			Severity:    "info",
			Scope:       "page_local",
			DetailsJSON: fmt.Sprintf(`{"statusCode":429,"host":%q}`, fr.host),
		})
	}

	// Check if HTML
	ct := strings.ToLower(fr.result.ContentType)
	isHTML := strings.Contains(ct, "text/html")

	if !isHTML {
		return pr
	}

	// Parse HTML
	page, parseErr := parser.ParseHTML(fr.result.Body, fr.result.FinalURL, fr.result.ResponseHeaders)
	if parseErr != nil {
		detailsJSON := fmt.Sprintf(`{"error":%q,"url":%q}`, parseErr.Error(), fr.url)
		e.db.InsertEvent(jobID, "parse_error", &detailsJSON, &fr.url)
		return pr
	}
	pr.page = page

	// forceRenderPatterns: mark pages matching patterns as JS-suspect
	// so they get flagged for browser rendering in hybrid mode.
	if e.config.MatchesForceRender(fr.url) && !page.JSSuspect {
		page.JSSuspect = true
	}

	newCount := pagesCrawled.Add(1)
	if int(newCount) > e.config.MaxPages {
		// Past limit — don't expand edges from this page
		return pr
	}

	// Build edges
	pr.edges = crawl.BuildEdges(fr.urlID, fr.result.FinalURL, page, e.scopeChecker, "static")

	// Detect page-local issues
	thresholds := issues.Thresholds{
		TitleMaxLength:       e.config.TitleMaxLength,
		TitleMinLength:       e.config.TitleMinLength,
		DescriptionMaxLength: e.config.DescriptionMaxLength,
		DescriptionMinLength: e.config.DescriptionMinLength,
		ThinContentThreshold: e.config.ThinContentThreshold,
		DeepPageThreshold:    e.config.DeepPageThreshold,
	}
	// Compute edge-based link stats for Batch B detectors
	var internalOutlinkCount int
	var nonDescriptiveCount int
	var nonDescriptiveExamples []string
	var internalNofollowCount int
	var unsafeCrossOriginCount int
	var unsafeCrossOriginExamples []string
	for _, edge := range pr.edges {
		if edge.RelationType != "link" {
			continue
		}
		if edge.IsInternal {
			internalOutlinkCount++
			if issues.IsNonDescriptiveAnchor(edge.AnchorText) {
				nonDescriptiveCount++
				if len(nonDescriptiveExamples) < 5 {
					nonDescriptiveExamples = append(nonDescriptiveExamples, strings.TrimSpace(edge.AnchorText))
				}
			}
			if strings.Contains(strings.ToLower(edge.RelFlagsJSON), "nofollow") {
				internalNofollowCount++
			}
		} else {
			// Check external links for unsafe cross-origin (target=_blank without noopener/noreferrer)
			relLower := strings.ToLower(edge.RelFlagsJSON)
			if edge.TargetAttr == "_blank" && !strings.Contains(relLower, "noopener") && !strings.Contains(relLower, "noreferrer") {
				unsafeCrossOriginCount++
				if len(unsafeCrossOriginExamples) < 5 {
					unsafeCrossOriginExamples = append(unsafeCrossOriginExamples, edge.DeclaredTargetURL)
				}
			}
		}
	}

	pageCtx := issues.PageContext{
		StatusCode:                   fr.result.StatusCode,
		RedirectHopCount:             len(fr.result.RedirectHops),
		RedirectLoopDetected:         fr.result.RedirectLoopDetected,
		RedirectHopsExceeded:         fr.result.RedirectHopsExceeded,
		TTFBMS:                       fr.result.TTFBMS,
		ContentType:                  fr.result.ContentType,
		Title:                        page.Title,
		TitleLength:                  page.TitleLength,
		MetaDescription:              page.MetaDescription,
		DescriptionLength:            page.DescriptionLength,
		MetaRobots:                   page.MetaRobots,
		XRobotsTag:                   page.XRobotsTag,
		CanonicalType:                page.CanonicalType,
		H1Count:                      len(page.Headings.H1),
		OGTitle:                      page.OpenGraph.Title,
		OGDescription:                page.OpenGraph.Description,
		OGImage:                      page.OpenGraph.Image,
		OGUrl:                        page.OpenGraph.URL,
		OGType:                       page.OpenGraph.Type,
		TwitterCard:                  page.TwitterCard.Card,
		TwitterTitle:                 page.TwitterCard.Title,
		TwitterDescription:           page.TwitterCard.Description,
		TwitterImage:                 page.TwitterCard.Image,
		JSONLDBlocks:                 len(page.JSONLDBlocks),
		MalformedJSONLD:              hasMalformedJSONLD(page.JSONLDBlocks),
		JSONLDRaw:                    marshalJSONLDBlocks(page.JSONLDBlocks),
		WordCount:                    page.ExtractedWordCount,
		MainContentWordCount:         page.MainContentWordCount,
		ImagesWithoutAlt:             countImagesWithoutAlt(page.Images),
		ImagesWithEmptyAlt:           countImagesWithEmptyAlt(page.Images),
		JSSuspect:                    page.JSSuspect,
		ScriptCount:                  page.ScriptCount,
		HasSPARoot:                   page.HasSPARoot,
		TitleOutsideHead:             page.TitleOutsideHead,
		MetaRobotsOutsideHead:        page.MetaRobotsOutsideHead,
		H1s:                          page.Headings.H1,
		H2s:                          page.Headings.H2,
		TitleCount:                   page.TitleCount,
		DescriptionCount:             page.DescriptionCount,
		MetaDescriptionOutsideHead:   page.MetaDescriptionOutsideHead,
		FirstHeadingLevel:            page.FirstHeadingLevel,
		H1AltTextOnly:                page.H1AltTextOnly,
		CanonicalCount:               page.CanonicalCount,
		CanonicalRaw:                 page.CanonicalRaw,
		CanonicalOutsideHead:         page.CanonicalOutsideHead,
		Images:                       page.Images,
		InternalOutlinkCount:         internalOutlinkCount,
		NonDescriptiveAnchorCount:    nonDescriptiveCount,
		NonDescriptiveAnchorExamples: nonDescriptiveExamples,
		InternalNofollowCount:        internalNofollowCount,
		PageURL:                      fr.url,
		ResponseHeaders:              fr.result.ResponseHeaders,
		Hreflangs:                    page.Hreflangs,
		FormInsecureActions:          page.FormInsecureActions,
		ProtocolRelativeCount:        page.ProtocolRelativeCount,
		HreflangOutsideHead:          page.HreflangOutsideHead,
		InvalidHTMLInHead:            page.InvalidHTMLInHead,
		HeadTagCount:                 page.HeadTagCount,
		BodyTagCount:                 page.BodyTagCount,
		BodySize:                     fr.result.BodySize,
		TextContent:                  page.ExtractedText,
		UnsafeCrossOriginCount:       unsafeCrossOriginCount,
		UnsafeCrossOriginExamples:    unsafeCrossOriginExamples,
	}
	pr.issues = issues.DetectPageLocalIssues(pageCtx, thresholds, fr.depth)

	// Collect discovered images for asset tracking
	for _, img := range page.Images {
		if img.Src == "" {
			continue
		}
		// Resolve relative URL against the page's final URL
		resolved := urlutil.ResolveReference(fr.result.FinalURL, img.Src)
		if resolved == "" {
			continue
		}
		imgNorm, normErr := urlutil.Normalize(resolved)
		if normErr != nil {
			continue
		}
		// Skip data: URLs
		if strings.HasPrefix(imgNorm, "data:") {
			continue
		}
		imgParsed, parseErr := url.Parse(imgNorm)
		if parseErr != nil {
			continue
		}
		imgHost := imgParsed.Hostname()
		imgInternal := false
		if e.scopeChecker != nil {
			imgInternal = e.scopeChecker.IsInScope(imgNorm)
		}
		pr.images = append(pr.images, discoveredImage{
			normalizedURL: imgNorm,
			host:          imgHost,
			isInternal:    imgInternal,
			sourceURLID:   fr.urlID,
		})
	}

	// Collect discovered assets (scripts, stylesheets, fonts, etc.) for asset tracking
	assetTypeToRef := map[string]string{
		"script":     "script_src",
		"stylesheet": "stylesheet_href",
		"font":       "font_preload",
		"icon":       "icon_href",
		"video":      "video_src",
		"audio":      "audio_src",
		"preload":    "preload_href",
	}
	for _, asset := range page.Assets {
		if asset.URL == "" {
			continue
		}
		resolved := urlutil.ResolveReference(fr.result.FinalURL, asset.URL)
		if resolved == "" {
			continue
		}
		assetNorm, normErr := urlutil.Normalize(resolved)
		if normErr != nil {
			continue
		}
		if strings.HasPrefix(assetNorm, "data:") {
			continue
		}
		assetParsed, parseErr := url.Parse(assetNorm)
		if parseErr != nil {
			continue
		}
		assetHost := assetParsed.Hostname()
		assetInternal := false
		if e.scopeChecker != nil {
			assetInternal = e.scopeChecker.IsInScope(assetNorm)
		}
		refType := assetTypeToRef[asset.Type]
		if refType == "" {
			refType = "other"
		}
		pr.assets = append(pr.assets, discoveredAsset{
			normalizedURL: assetNorm,
			host:          assetHost,
			isInternal:    assetInternal,
			sourceURLID:   fr.urlID,
			refType:       refType,
		})
	}

	// Expand frontier with discovered in-scope links
	for _, edge := range pr.edges {
		if edge.RelationType != "link" {
			continue
		}
		if !edge.IsInternal {
			continue
		}

		normalized := edge.NormalizedTargetURL
		if normalized == "" {
			continue
		}

		// MaxDepth check
		newDepth := fr.depth + 1
		if newDepth > e.config.MaxDepth {
			continue
		}

		// MaxPages check
		if int(pagesCrawled.Load()) >= e.config.MaxPages {
			continue
		}

		// Crawl trap: repeated path segments
		if urlutil.HasRepeatedPathSegments(normalized) {
			continue
		}

		// Crawl trap: query variant limit
		parsed, parseErr := url.Parse(normalized)
		if parseErr != nil {
			continue
		}
		pathKey := parsed.Path
		if parsed.RawQuery != "" {
			queryVariantsMu.Lock()
			queryVariants[pathKey]++
			count := queryVariants[pathKey]
			queryVariantsMu.Unlock()
			if count > e.config.MaxQueryVariantsPerPath {
				detailsJSON := fmt.Sprintf(`{"path":%q,"queryVariants":%d,"limit":%d,"url":%q}`,
					pathKey, count, e.config.MaxQueryVariantsPerPath, normalized)
				e.db.InsertIssue(storage.IssueInput{
					JobID:       jobID,
					URLID:       nil,
					IssueType:   "crawl_trap_suspected",
					Severity:    "info",
					Scope:       "page_local",
					DetailsJSON: &detailsJSON,
				})
				continue
			}
		}

		targetHost := parsed.Hostname()
		urlID, upsertErr := e.db.UpsertURL(jobID, normalized, targetHost, "queued", true, "link")
		if upsertErr != nil {
			continue
		}

		urlsDiscovered.Add(1)

		q.Push(frontier.Item{
			URLID:         urlID,
			NormalizedURL: normalized,
			Host:          targetHost,
			Depth:         newDepth,
		})
	}

	return pr
}

// sitemapGapEscalation detects sitemap URLs with no inbound static HTML links,
// re-renders key pages with the browser to discover JS-only navigation, and
// queues any newly discovered URLs. Returns the number of new URLs queued.
func (e *Engine) sitemapGapEscalation(ctx context.Context, jobID string) int {
	// 1. Get all sitemap entry URLs for this job
	sitemapURLs := map[string]bool{}
	rows, err := e.db.Query(
		`SELECT DISTINCT se.url FROM sitemap_entries se WHERE se.job_id = ?`,
		jobID,
	)
	if err != nil {
		log.Printf("engine: sitemap gap: failed to query sitemap entries: %v", err)
		return 0
	}
	for rows.Next() {
		var u string
		if scanErr := rows.Scan(&u); scanErr == nil {
			// Normalize for consistent comparison
			if norm, normErr := urlutil.Normalize(u); normErr == nil {
				sitemapURLs[norm] = true
			}
		}
	}
	rows.Close()

	if len(sitemapURLs) == 0 {
		return 0
	}

	// 2. Get all URLs that have at least one inbound static HTML link edge (excluding self-links)
	rows, err = e.db.Query(
		`SELECT e.declared_target_url, u_src.normalized_url AS source_url
		 FROM edges e
		 JOIN urls u_src ON u_src.id = e.source_url_id AND u_src.job_id = e.job_id
		 WHERE e.job_id = ? AND e.discovery_mode = 'static' AND e.is_internal = 1 AND e.relation_type = 'link'`,
		jobID,
	)
	if err != nil {
		log.Printf("engine: sitemap gap: failed to query static edges: %v", err)
		return 0
	}
	linkedURLs := map[string]bool{}
	for rows.Next() {
		var targetURL, sourceURL string
		if scanErr := rows.Scan(&targetURL, &sourceURL); scanErr == nil {
			if norm, normErr := urlutil.Normalize(targetURL); normErr == nil {
				// Exclude self-links (source page linking to itself with fragment)
				if norm != sourceURL {
					linkedURLs[norm] = true
				}
			}
		}
	}
	rows.Close()

	// 3. Find the gap: sitemap URLs with NO static inbound links
	var gap []string
	for u := range sitemapURLs {
		if !linkedURLs[u] {
			gap = append(gap, u)
		}
	}

	if len(gap) == 0 {
		return 0
	}

	log.Printf("engine: sitemap gap: %d sitemap URLs have no static inbound links", len(gap))

	// 4. Check renderer availability
	if e.renderer == nil {
		log.Printf("engine: sitemap gap detected but no renderer available, skipping escalation")
		detailsJSON := fmt.Sprintf(`{"gapCount":%d,"pagesReRendered":0,"newLinksFound":0,"newURLsDiscovered":0,"reason":"no_renderer"}`, len(gap))
		e.db.InsertEvent(jobID, "sitemap_gap_escalation", &detailsJSON, nil)
		return 0
	}

	// 5. Get key pages to re-render (top 10 by outbound link count)
	rows, err = e.db.Query(
		`SELECT u.id, u.normalized_url
		 FROM urls u
		 WHERE u.job_id = ? AND u.status = 'fetched' AND u.is_internal = 1
		 ORDER BY (SELECT COUNT(*) FROM edges e WHERE e.job_id = u.job_id AND e.source_url_id = u.id) DESC
		 LIMIT 5`,
		jobID,
	)
	if err != nil {
		log.Printf("engine: sitemap gap: failed to query key pages: %v", err)
		return 0
	}
	type keyPage struct {
		urlID int64
		url   string
	}
	var keyPages []keyPage
	for rows.Next() {
		var kp keyPage
		if scanErr := rows.Scan(&kp.urlID, &kp.url); scanErr == nil {
			keyPages = append(keyPages, kp)
		}
	}
	rows.Close()

	if len(keyPages) == 0 {
		return 0
	}

	// Build gap set for fast lookup
	gapSet := map[string]bool{}
	for _, u := range gap {
		gapSet[u] = true
	}

	// 6. Re-render each key page with the browser
	newLinksFound := 0
	newURLsDiscovered := 0
	pagesReRendered := 0

	for _, kp := range keyPages {
		if ctx.Err() != nil {
			break
		}

		// Try Playwright first (better menu discovery via real click handlers),
		// fall back to chromedp if Playwright is unavailable or fails.
		var renderHTML string
		var renderFinalURL string

		var playwrightLinks []string
		if renderer.IsPlaywrightAvailable() {
			pwResult, pwErr := renderer.RenderWithPlaywright(ctx, kp.url)
			if pwErr != nil {
				log.Printf("engine: sitemap gap: playwright render failed for %s: %v, falling back to chromedp", kp.url, pwErr)
			} else {
				renderHTML = pwResult.HTML
				renderFinalURL = kp.url
				playwrightLinks = pwResult.Links // Links collected incrementally during menu clicks
			}
		}

		// Chromedp fallback
		if renderHTML == "" {
			renderResult, renderErr := e.renderer.RenderWithOptions(ctx, kp.url, renderer.RenderOptions{
				DiscoverMenus: true,
			})
			if renderErr != nil {
				log.Printf("engine: sitemap gap: render failed for %s: %v", kp.url, renderErr)
				continue
			}
			renderHTML = renderResult.HTML
			renderFinalURL = renderResult.FinalURL
		}
		pagesReRendered++

		// Parse the rendered HTML (includes lazy-loaded content after full scroll)
		page, parseErr := parser.ParseHTML([]byte(renderHTML), renderFinalURL, http.Header{})
		if parseErr != nil {
			log.Printf("engine: sitemap gap: parse failed for rendered %s: %v", kp.url, parseErr)
			continue
		}

		// Update the page record if browser rendering found more content (lazy loading)
		if page.ExtractedWordCount > 0 {
			e.db.Exec(`
				UPDATE pages SET
					word_count = MAX(COALESCE(word_count, 0), ?),
					main_content_word_count = MAX(COALESCE(main_content_word_count, 0), ?),
					content_hash = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE content_hash END,
					h1_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h1_json END,
					h2_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h2_json END,
					images_json = CASE WHEN ? > COALESCE((SELECT COUNT(*) FROM json_each(images_json)), 0) THEN ? ELSE images_json END
				WHERE job_id = ? AND url_id = ?`,
				page.ExtractedWordCount, page.MainContentWordCount,
				page.ExtractedWordCount, page.ContentHash,
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H1),
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H2),
				len(page.Images), marshalImages(page.Images),
				jobID, kp.urlID,
			)
		}

		// Build edges from rendered DOM
		renderedEdges := crawl.BuildEdges(kp.urlID, renderFinalURL, page, e.scopeChecker, "browser")

		// Find NEW links: in rendered edges but not already in static edges for this source
		// We only count edges to OTHER pages (exclude self-links that normalize to same URL)
		existingEdges := map[string]bool{}
		edgeRows, edgeErr := e.db.Query(
			`SELECT declared_target_url FROM edges WHERE job_id = ? AND source_url_id = ? AND discovery_mode = 'static'`,
			jobID, kp.urlID,
		)
		if edgeErr == nil {
			for edgeRows.Next() {
				var target string
				if scanErr := edgeRows.Scan(&target); scanErr == nil {
					if norm, normErr := urlutil.Normalize(target); normErr == nil {
						existingEdges[norm] = true
					}
				}
			}
			edgeRows.Close()
		}

		for _, edge := range renderedEdges {
			if edge.RelationType != "link" || !edge.IsInternal {
				continue
			}
			norm := edge.NormalizedTargetURL
			if norm == "" {
				continue
			}
			if existingEdges[norm] {
				continue
			}

			newLinksFound++

			// Persist the new browser-discovered edge
			parsed, parseErr := url.Parse(norm)
			if parseErr != nil {
				continue
			}
			targetHost := parsed.Hostname()
			targetURLID, upsertErr := e.db.UpsertURL(jobID, norm, targetHost, "discovered", true, "browser")
			if upsertErr != nil {
				continue
			}

			var anchorText *string
			if edge.AnchorText != "" {
				anchorText = &edge.AnchorText
			}
			var relFlags *string
			if edge.RelFlagsJSON != "" {
				relFlags = &edge.RelFlagsJSON
			}

			e.db.Exec(
				`INSERT INTO edges (job_id, source_url_id, normalized_target_url_id,
					source_kind, relation_type, rel_flags_json, discovery_mode,
					anchor_text, is_internal, declared_target_url)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				jobID, kp.urlID, targetURLID,
				"rendered_dom", edge.RelationType, relFlags, "browser",
				anchorText, 1, edge.DeclaredTargetURL,
			)

			// If this URL is in the gap set, it's a successful escalation
			if gapSet[norm] {
				newURLsDiscovered++
				log.Printf("engine: sitemap gap: browser discovered gap URL %s via %s", norm, kp.url)
			}
		}

		// Also check Playwright-collected links directly (covers menus that close after click)
		for _, pwLink := range playwrightLinks {
			norm, normErr := urlutil.Normalize(pwLink)
			if normErr != nil || norm == "" {
				continue
			}
			if existingEdges[norm] {
				continue
			}
			if !e.scopeChecker.IsInScope(norm) {
				continue
			}
			// Already found via rendered edges?
			alreadyCounted := false
			for _, edge := range renderedEdges {
				if edge.NormalizedTargetURL == norm {
					alreadyCounted = true
					break
				}
			}
			if alreadyCounted {
				continue
			}

			newLinksFound++
			parsed, parseErr := url.Parse(norm)
			if parseErr != nil {
				continue
			}
			targetHost := parsed.Hostname()
			targetURLID, upsertErr := e.db.UpsertURL(jobID, norm, targetHost, "discovered", true, "browser")
			if upsertErr != nil {
				continue
			}
			e.db.Exec(
				`INSERT INTO edges (job_id, source_url_id, normalized_target_url_id,
					source_kind, relation_type, discovery_mode,
					is_internal, declared_target_url)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				jobID, kp.urlID, targetURLID,
				"rendered_dom", "link", "browser",
				1, pwLink,
			)
			if gapSet[norm] {
				newURLsDiscovered++
				log.Printf("engine: sitemap gap: playwright link discovered gap URL %s via %s", norm, kp.url)
			}
		}
	}

	// 7. Log the escalation event
	detailsJSON := fmt.Sprintf(
		`{"gapCount":%d,"pagesReRendered":%d,"newLinksFound":%d,"newURLsDiscovered":%d}`,
		len(gap), pagesReRendered, newLinksFound, newURLsDiscovered,
	)
	e.db.InsertEvent(jobID, "sitemap_gap_escalation", &detailsJSON, nil)

	return newURLsDiscovered
}

// persistItem saves a single crawl result to the database inside a single transaction.
// headCheckAssets performs HEAD requests on all discovered asset URLs
// (images, scripts, stylesheets, fonts, media, etc.)
// and stores the results in the assets table. Caps at 2000 unique assets.
func (e *Engine) headCheckAssets(ctx context.Context, jobID string) {
	// Query all distinct asset URLs from asset_references for this job
	rows, err := e.db.Query(
		`SELECT DISTINCT ar.asset_url_id, u.normalized_url
		 FROM asset_references ar
		 JOIN urls u ON u.id = ar.asset_url_id
		 WHERE ar.job_id = ?
		 LIMIT 2000`,
		jobID,
	)
	if err != nil {
		log.Printf("engine: failed to query assets for HEAD checking: %v", err)
		return
	}
	defer rows.Close()

	type assetTarget struct {
		urlID int64
		url   string
	}
	var targets []assetTarget
	for rows.Next() {
		var t assetTarget
		if scanErr := rows.Scan(&t.urlID, &t.url); scanErr != nil {
			continue
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		log.Printf("engine: error iterating assets: %v", err)
	}

	if len(targets) == 0 {
		return
	}

	e.emitPhase(jobID, "asset_checks", fmt.Sprintf("HEAD-checking %d discovered assets", len(targets)))
	log.Printf("engine: HEAD-checking %d discovered assets", len(targets))

	// Use a small worker pool to avoid overwhelming hosts
	const headWorkers = 4
	work := make(chan assetTarget, len(targets))
	for _, t := range targets {
		work <- t
	}
	close(work)

	var wg sync.WaitGroup
	for range headWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				if ctx.Err() != nil {
					return
				}
				headResult, headErr := e.fetcher.HeadContext(ctx, t.url)
				var contentType *string
				var statusCode *int
				var contentLength *int64
				if headErr == nil && headResult != nil {
					contentType = strPtr(headResult.ContentType)
					statusCode = intPtr(headResult.StatusCode)
					// Extract Content-Length from response headers
					if clStr := headResult.ResponseHeaders.Get("Content-Length"); clStr != "" {
						if cl, parseErr := strconv.ParseInt(clStr, 10, 64); parseErr == nil {
							contentLength = &cl
						}
					}
				}
				if _, insertErr := e.db.InsertAsset(storage.AssetInput{
					JobID:         jobID,
					URLID:         t.urlID,
					ContentType:   contentType,
					StatusCode:    statusCode,
					ContentLength: contentLength,
				}); insertErr != nil {
					// May fail on duplicate; that's fine
					continue
				}
			}
		}()
	}
	wg.Wait()
	log.Printf("engine: completed HEAD-checking assets")
}

func (e *Engine) persistItem(ctx context.Context, jobID string, item persistItem) error {
	fr := item.fetchResult
	seq := item.fetchSeq

	if fr.result == nil && fr.err != nil {
		return nil
	}

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op if committed

	// --- Resolve final URL ID (may upsert) ---
	var finalURLID *int64
	if fr.result != nil && fr.result.FinalURL != fr.url {
		parsed, parseErr := url.Parse(fr.result.FinalURL)
		if parseErr == nil {
			finalInScope := e.scopeChecker.IsInScope(fr.result.FinalURL)
			finalStatus := "fetched"
			if !finalInScope {
				finalStatus = "out_of_scope"
			}
			fid, upsertErr := txUpsertURL(tx, jobID, fr.result.FinalURL, parsed.Hostname(), finalStatus, finalInScope, "redirect")
			if upsertErr == nil {
				finalURLID = &fid
			}
		}
	}

	// --- Build fetch fields ---
	var fetchErr *string
	statusCode := 0
	var bodySize int64
	var contentType, contentEncoding string
	var ttfbMS int64
	redirectHopCount := 0
	var headersJSON string

	if fr.result != nil {
		statusCode = fr.result.StatusCode
		bodySize = fr.result.BodySize
		contentType = fr.result.ContentType
		contentEncoding = fr.result.ContentEncoding
		ttfbMS = fr.result.TTFBMS
		redirectHopCount = len(fr.result.RedirectHops)

		if h := fr.result.ResponseHeaders; h != nil {
			hBytes, _ := json.Marshal(h)
			headersJSON = string(hBytes)
		}
	}
	if fr.err != nil {
		s := fr.err.Error()
		fetchErr = &s
	}

	// --- Insert fetch ---
	result, insertErr := tx.ExecContext(ctx,
		`INSERT INTO fetches (job_id, fetch_seq, requested_url_id, final_url_id,
			status_code, redirect_hop_count, ttfb_ms, response_body_size,
			content_type, content_encoding, response_headers_json,
			http_method, fetch_kind, render_mode, render_params_json, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, seq, fr.urlID, finalURLID,
		statusCode, redirectHopCount, ttfbMS, bodySize,
		contentType, contentEncoding, headersJSON,
		"GET", "full", "static", nil, fetchErr,
	)
	if insertErr != nil {
		return fmt.Errorf("inserting fetch: %w", insertErr)
	}
	fetchID, _ := result.LastInsertId()

	// --- Insert redirect hops ---
	if fr.result != nil {
		for _, hop := range fr.result.RedirectHops {
			if _, hopErr := tx.ExecContext(ctx,
				`INSERT INTO redirect_hops (job_id, fetch_id, hop_index, status_code, from_url, to_url) VALUES (?, ?, ?, ?, ?, ?)`,
				jobID, fetchID, hop.HopIndex, hop.StatusCode, hop.FromURL, hop.ToURL,
			); hopErr != nil {
				return fmt.Errorf("inserting redirect hop: %w", hopErr)
			}
		}
	}

	ct := ""
	if fr.result != nil {
		ct = strings.ToLower(fr.result.ContentType)
	}
	isHTML := strings.Contains(ct, "text/html")

	// --- Non-HTML: record as asset ---
	if !isHTML && fr.result != nil && fr.err == nil {
		if _, assetErr := tx.ExecContext(ctx,
			`INSERT INTO assets (job_id, url_id, content_type, status_code, content_length) VALUES (?, ?, ?, ?, ?)`,
			jobID, fr.urlID, fr.result.ContentType, fr.result.StatusCode, fr.result.BodySize,
		); assetErr != nil {
			return fmt.Errorf("inserting asset: %w", assetErr)
		}
		return tx.Commit()
	}

	// --- HTML: insert page record ---
	if isHTML && item.page != nil {
		if pageErr := txInsertPage(ctx, tx, jobID, fr.urlID, fetchID, fr.depth, item.page); pageErr != nil {
			return fmt.Errorf("inserting page: %w", pageErr)
		}
	}

	// --- Insert edges ---
	for _, edge := range item.edges {
		parsed, parseErr := url.Parse(edge.NormalizedTargetURL)
		if parseErr != nil {
			continue
		}
		targetHost := parsed.Hostname()
		targetURLID, upsertErr := txUpsertURL(tx, jobID, edge.NormalizedTargetURL, targetHost, "discovered", edge.IsInternal, "link")
		if upsertErr != nil {
			continue
		}

		var anchorText *string
		if edge.AnchorText != "" {
			anchorText = &edge.AnchorText
		}
		var relFlags *string
		if edge.RelFlagsJSON != "" {
			relFlags = &edge.RelFlagsJSON
		}

		boolToInt := 0
		if edge.IsInternal {
			boolToInt = 1
		}

		// HEAD request for out-of-scope canonical/hreflang targets
		var targetStatusCode *int
		if !edge.IsInternal && (edge.RelationType == "canonical" || edge.RelationType == "hreflang") {
			headResult, headErr := e.fetcher.HeadContext(ctx, edge.NormalizedTargetURL)
			if headErr == nil && headResult != nil {
				targetStatusCode = &headResult.StatusCode
			}
		}

		if _, edgeErr := tx.ExecContext(ctx,
			`INSERT INTO edges (job_id, source_url_id, normalized_target_url_id,
				source_kind, relation_type, rel_flags_json, discovery_mode,
				anchor_text, is_internal, declared_target_url, target_status_code)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			jobID, edge.SourceURLID, targetURLID,
			edge.SourceKind, edge.RelationType, relFlags, edge.DiscoveryMode,
			anchorText, boolToInt, edge.DeclaredTargetURL, targetStatusCode,
		); edgeErr != nil {
			return fmt.Errorf("inserting edge: %w", edgeErr)
		}
	}

	// --- Insert issues ---
	for _, issue := range item.issues {
		details := issue.DetailsJSON
		if _, issueErr := tx.ExecContext(ctx,
			`INSERT INTO issues (job_id, url_id, issue_type, severity, scope, details_json) VALUES (?, ?, ?, ?, ?, ?)`,
			jobID, &fr.urlID, issue.IssueType, issue.Severity, issue.Scope, &details,
		); issueErr != nil {
			return fmt.Errorf("inserting issue: %w", issueErr)
		}
	}

	// --- Insert image asset references ---
	for _, img := range item.images {
		imgURLID, upsertErr := txUpsertURL(tx, jobID, img.normalizedURL, img.host, "discovered", img.isInternal, "asset")
		if upsertErr != nil {
			continue
		}
		if _, refErr := tx.ExecContext(ctx,
			`INSERT INTO asset_references (job_id, asset_url_id, source_page_url_id, reference_type)
			 VALUES (?, ?, ?, ?)`,
			jobID, imgURLID, img.sourceURLID, "img_src",
		); refErr != nil {
			// Duplicate references are possible; ignore unique constraint errors
			continue
		}
	}

	// --- Insert non-image asset references (scripts, stylesheets, fonts, etc.) ---
	for _, asset := range item.assets {
		assetURLID, upsertErr := txUpsertURL(tx, jobID, asset.normalizedURL, asset.host, "discovered", asset.isInternal, "asset")
		if upsertErr != nil {
			continue
		}
		if _, refErr := tx.ExecContext(ctx,
			`INSERT INTO asset_references (job_id, asset_url_id, source_page_url_id, reference_type)
			 VALUES (?, ?, ?, ?)`,
			jobID, assetURLID, asset.sourceURLID, asset.refType,
		); refErr != nil {
			// Duplicate references are possible; ignore unique constraint errors
			continue
		}
	}

	return tx.Commit()
}

// txUpsertURL upserts a URL within a transaction and returns its ID.
func txUpsertURL(tx *sql.Tx, jobID, normalizedURL, host, status string, isInternal bool, discoveredVia string) (int64, error) {
	isInternalInt := 0
	if isInternal {
		isInternalInt = 1
	}
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO urls (job_id, normalized_url, host, status, is_internal, discovered_via)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		jobID, normalizedURL, host, status, isInternalInt, discoveredVia,
	)
	if err != nil {
		return 0, fmt.Errorf("upserting URL %q: %w", normalizedURL, err)
	}

	var id int64
	err = tx.QueryRow(
		`SELECT id FROM urls WHERE job_id = ? AND normalized_url = ?`,
		jobID, normalizedURL,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("fetching ID for URL %q: %w", normalizedURL, err)
	}
	return id, nil
}

// txInsertPage creates a page record within a transaction.
func txInsertPage(ctx context.Context, tx *sql.Tx, jobID string, urlID, fetchID int64, depth int, page *parser.ParseResult) error {
	title := strPtr(page.Title)
	titleLen := intPtr(page.TitleLength)
	metaDesc := strPtr(page.MetaDescription)
	metaDescLen := intPtr(page.DescriptionLength)
	metaRobots := strPtr(page.MetaRobots)
	xRobots := strPtr(page.XRobotsTag)
	canonical := strPtr(page.CanonicalResolved)

	var canonicalIsSelf *int
	if page.CanonicalType == "self" {
		v := 1
		canonicalIsSelf = &v
	} else if page.CanonicalType == "cross" {
		v := 0
		canonicalIsSelf = &v
	}

	var relNext, relPrev *string
	if page.RelNext != nil {
		relNext = &page.RelNext.Resolved
	}
	if page.RelPrev != nil {
		relPrev = &page.RelPrev.Resolved
	}

	hreflangJSON := jsonStrPtr(page.Hreflangs)
	h1JSON := jsonStrPtr(page.Headings.H1)
	h2JSON := jsonStrPtr(page.Headings.H2)
	h3JSON := jsonStrPtr(page.Headings.H3)
	h4JSON := jsonStrPtr(page.Headings.H4)
	h5JSON := jsonStrPtr(page.Headings.H5)
	h6JSON := jsonStrPtr(page.Headings.H6)

	ogTitle := strPtr(page.OpenGraph.Title)
	ogDesc := strPtr(page.OpenGraph.Description)
	ogImage := strPtr(page.OpenGraph.Image)
	ogURL := strPtr(page.OpenGraph.URL)
	ogType := strPtr(page.OpenGraph.Type)

	twitterCard := strPtr(page.TwitterCard.Card)
	twitterTitle := strPtr(page.TwitterCard.Title)
	twitterDesc := strPtr(page.TwitterCard.Description)
	twitterImage := strPtr(page.TwitterCard.Image)

	var jsonldRaw *string
	if len(page.JSONLDBlocks) > 0 {
		raw, _ := json.Marshal(page.JSONLDBlocks)
		s := string(raw)
		jsonldRaw = &s
	}
	jsonldTypes := jsonStrPtr(page.JSONLDTypes)
	imagesJSON := jsonStrPtr(page.Images)
	wordCount := intPtr(page.ExtractedWordCount)
	mainWC := intPtr(page.MainContentWordCount)
	contentHash := strPtr(page.ContentHash)

	jsSuspect := 0
	if page.JSSuspect {
		jsSuspect = 1
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO pages (job_id, url_id, fetch_id, depth,
			title, title_length, meta_description, meta_description_length,
			meta_robots, x_robots_tag, indexability_state,
			canonical_url, canonical_is_self, rel_next_url, rel_prev_url,
			hreflang_json,
			h1_json, h2_json, h3_json, h4_json, h5_json, h6_json,
			og_title, og_description, og_image, og_url, og_type,
			twitter_card, twitter_title, twitter_description, twitter_image,
			jsonld_raw, jsonld_types_json,
			images_json, word_count, main_content_word_count,
			content_hash, js_suspect)
		 VALUES (?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?)`,
		jobID, urlID, fetchID, depth,
		title, titleLen, metaDesc, metaDescLen,
		metaRobots, xRobots, page.IndexabilityState,
		canonical, canonicalIsSelf, relNext, relPrev,
		hreflangJSON,
		h1JSON, h2JSON, h3JSON, h4JSON, h5JSON, h6JSON,
		ogTitle, ogDesc, ogImage, ogURL, ogType,
		twitterCard, twitterTitle, twitterDesc, twitterImage,
		jsonldRaw, jsonldTypes,
		imagesJSON, wordCount, mainWC,
		contentHash, jsSuspect,
	)
	return err
}

// runLighthouseAudits runs PageSpeed Insights API for all crawled pages
// using a worker pool of 4 goroutines for parallel execution.
// Results are stored as crawl events with type "psi_audit".
func (e *Engine) runLighthouseAudits(ctx context.Context, jobID string) {
	rows, err := e.db.Query(
		`SELECT u.normalized_url FROM pages p JOIN urls u ON u.id = p.url_id WHERE p.job_id = ? LIMIT 50`,
		jobID,
	)
	if err != nil {
		log.Printf("engine: PSI audit query failed: %v", err)
		return
	}
	var urls []string
	for rows.Next() {
		var u string
		if scanErr := rows.Scan(&u); scanErr == nil {
			urls = append(urls, u)
		}
	}
	rows.Close()

	if len(urls) == 0 {
		return
	}

	// Only mobile by default — desktop is optional and halves API calls
	strategies := []string{"mobile"}
	if e.config.PSIDesktop {
		strategies = append(strategies, "desktop")
	}

	log.Printf("engine: running PSI audits on %d pages (%d strategies, %d total calls)",
		len(urls), len(strategies), len(urls)*len(strategies))

	// Build work items
	type psiWork struct {
		url      string
		strategy string
	}
	work := make(chan psiWork, len(urls)*len(strategies))
	for _, pageURL := range urls {
		for _, strategy := range strategies {
			work <- psiWork{url: pageURL, strategy: strategy}
		}
	}
	close(work)

	// Rate limiter: 1 call per 500ms across all workers (PSI allows 25K/day)
	rateTicker := time.NewTicker(1000 * time.Millisecond)
	defer rateTicker.Stop()
	// Rate limiting handled by rateTicker in worker loop

	var mu sync.Mutex
	var audited, failed int

	const psiWorkers = 2
	var wg sync.WaitGroup
	for range psiWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if ctx.Err() != nil {
					return
				}

				// Rate limit: wait for ticker
				select {
				case <-ctx.Done():
					return
				case <-rateTicker.C:
				}

				result, psiErr := lighthouse.FetchPSI(ctx, item.url, e.config.PSIAPIKey, item.strategy)
				if psiErr != nil {
					mu.Lock()
					failed++
					mu.Unlock()
					log.Printf("engine: PSI audit failed for %s (%s): %v", item.url, item.strategy, psiErr)
					continue
				}

				detailsBytes, _ := json.Marshal(result)
				details := string(detailsBytes)
				urlStr := item.url
				e.db.InsertEvent(jobID, "psi_audit", &details, &urlStr)

				mu.Lock()
				audited++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	log.Printf("engine: completed PSI audits (%d results, %d failed)", audited, failed)
}

// runAxeAudits runs axe-core accessibility audits on all crawled pages
// using a single Playwright browser instance (batch mode).
// Results are stored as crawl events with type "axe_audit".
func (e *Engine) runAxeAudits(ctx context.Context, jobID string) {
	rows, err := e.db.Query(
		`SELECT u.normalized_url FROM pages p JOIN urls u ON u.id = p.url_id WHERE p.job_id = ? LIMIT 50`,
		jobID,
	)
	if err != nil {
		log.Printf("engine: Axe audit query failed: %v", err)
		return
	}
	var urls []string
	for rows.Next() {
		var u string
		if scanErr := rows.Scan(&u); scanErr == nil {
			urls = append(urls, u)
		}
	}
	rows.Close()

	if len(urls) == 0 {
		return
	}

	log.Printf("engine: running Axe accessibility audits on %d pages (batch mode)", len(urls))

	// Run all URLs in a single batch — one browser launch for all pages
	results, batchErr := renderer.RunAxeAuditBatch(ctx, urls)
	if batchErr != nil {
		log.Printf("engine: Axe batch audit failed: %v", batchErr)
		return
	}

	audited := 0
	for _, result := range results {
		detailsBytes, _ := json.Marshal(result)
		details := string(detailsBytes)
		urlStr := result.URL
		e.db.InsertEvent(jobID, "axe_audit", &details, &urlStr)
		audited++
	}

	log.Printf("engine: completed Axe accessibility audits (%d results)", audited)
}

// failJob marks a job as failed with the given error.
func (e *Engine) failJob(jobID string, err error) error {
	errMsg := err.Error()
	e.db.UpdateJobFinished(jobID, "failed", &errMsg)
	return err
}

// Helper functions

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtr(i int) *int {
	return &i
}

func jsonStrPtr(v any) *string {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	s := string(data)
	return &s
}

// checkMarkdownNegotiation tests if crawled pages support Accept: text/markdown
// content negotiation (agent-friendly sites). Creates events with results.
func (e *Engine) checkMarkdownNegotiation(ctx context.Context, jobID string) {
	rows, err := e.db.Query(`
		SELECT u.normalized_url
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		WHERE p.job_id = ?
	`, jobID)
	if err != nil {
		log.Printf("engine: markdown negotiation query failed: %v", err)
		return
	}
	defer rows.Close()

	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return
	}

	log.Printf("engine: checking markdown content negotiation on %d pages", len(urls))

	client := &http.Client{Timeout: 5 * time.Second} // shorter timeout: most servers respond fast or 404

	type mdResult struct {
		URL           string `json:"url"`
		Supports      bool   `json:"supportsMarkdown"`
		ContentType   string `json:"contentType"`
		ContentLength int64  `json:"contentLength"`
	}

	// Parallelize: 16 concurrent requests turn a 10-min sequential pass into
	// ~40s for 2K pages. The job table progress counters keep moving thanks to
	// the periodic flush ticker, and we also emit a progress phase event
	// every `progressEvery` pages so the live log keeps moving.
	const (
		workers       = 16
		progressEvery = 200
	)

	var (
		results    = make([]mdResult, len(urls))
		supported  atomic.Int64
		processed  atomic.Int64
		wg         sync.WaitGroup
		urlIdxChan = make(chan int, workers*2)
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range urlIdxChan {
				if ctx.Err() != nil {
					return
				}
				pageURL := urls[idx]
				res := mdResult{URL: pageURL}

				req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
				if err == nil {
					req.Header.Set("Accept", "text/markdown")
					req.Header.Set("User-Agent", e.config.UserAgent)
					if resp, doErr := client.Do(req); doErr == nil {
						res.ContentType = resp.Header.Get("Content-Type")
						res.ContentLength = resp.ContentLength
						res.Supports = strings.Contains(strings.ToLower(res.ContentType), "text/markdown")
						resp.Body.Close()
						if res.Supports {
							supported.Add(1)
						}
					}
				}
				results[idx] = res

				if n := processed.Add(1); n%progressEvery == 0 || int(n) == len(urls) {
					e.emitPhase(jobID, "markdown_progress", fmt.Sprintf("checked %d/%d pages", n, len(urls)))
				}
			}
		}()
	}

	for i := range urls {
		select {
		case <-ctx.Done():
			close(urlIdxChan)
			wg.Wait()
			return
		case urlIdxChan <- i:
		}
	}
	close(urlIdxChan)
	wg.Wait()

	total := int(processed.Load())
	supportedCount := int(supported.Load())

	// Store as a crawl event
	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"totalChecked": total,
		"supported":    supportedCount,
		"unsupported":  total - supportedCount,
		"pages":        results,
	})
	details := string(detailsJSON)
	e.db.InsertEvent(jobID, "markdown_negotiation", &details, nil)

	// Build all issues in memory then write them in a single transaction.
	// Calling InsertIssue 2.5K times in a loop would acquire/release the
	// (single) SQLite write connection 2.5K times, blocking other queries.
	issueBatch := make([]storage.IssueInput, 0, len(results))
	for _, r := range results {
		d, _ := json.Marshal(map[string]interface{}{
			"url":         r.URL,
			"contentType": r.ContentType,
		})
		ds := string(d)
		itype := "missing_markdown_negotiation"
		if r.Supports {
			itype = "supports_markdown_negotiation"
		}
		issueBatch = append(issueBatch, storage.IssueInput{
			JobID:       jobID,
			IssueType:   itype,
			Severity:    "info",
			Scope:       "page_local",
			DetailsJSON: &ds,
		})
	}
	if err := e.db.InsertIssuesBatch(issueBatch); err != nil {
		log.Printf("engine: markdown negotiation issues batch insert failed: %v", err)
	}

	log.Printf("engine: markdown negotiation: %d/%d pages support Accept: text/markdown", supportedCount, total)
}

// runTextQualityChecks runs LanguageTool on all crawled pages and creates
// issues for spelling/grammar errors found.
func (e *Engine) runTextQualityChecks(ctx context.Context, jobID string) {
	client := textquality.NewLTClient(e.config.LanguageToolURL)
	if !client.IsAvailable(ctx) {
		log.Printf("engine: LanguageTool not available at %s, skipping text quality checks", e.config.LanguageToolURL)
		return
	}

	rows, err := e.db.Query(`
		SELECT p.url_id, u.normalized_url, f.id as fetch_id
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		JOIN fetches f ON f.id = p.fetch_id AND f.job_id = p.job_id
		WHERE p.job_id = ? AND p.word_count > 0
	`, jobID)
	if err != nil {
		log.Printf("engine: text quality query failed: %v", err)
		return
	}
	defer rows.Close()

	type pageRef struct {
		urlID   int64
		url     string
		fetchID int64
	}
	var pages []pageRef
	for rows.Next() {
		var pr pageRef
		if err := rows.Scan(&pr.urlID, &pr.url, &pr.fetchID); err != nil {
			continue
		}
		pages = append(pages, pr)
	}

	if len(pages) == 0 {
		return
	}

	// Build custom dictionary from brand names found in titles and H1s
	customDict := map[string]bool{}
	dictRows, dictErr := e.db.Query(`
		SELECT DISTINCT title, h1_json FROM pages WHERE job_id = ?
	`, jobID)
	if dictErr == nil {
		for dictRows.Next() {
			var title, h1JSON sql.NullString
			if err := dictRows.Scan(&title, &h1JSON); err != nil {
				continue
			}
			// Extract words from titles
			if title.Valid {
				for _, word := range strings.Fields(title.String) {
					cleaned := strings.Trim(word, ".,;:!?|()-\"'")
					if len(cleaned) > 2 {
						customDict[cleaned] = true
					}
				}
			}
			// Extract words from H1s
			if h1JSON.Valid {
				var h1s []string
				if err := json.Unmarshal([]byte(h1JSON.String), &h1s); err == nil {
					for _, h1 := range h1s {
						for _, word := range strings.Fields(h1) {
							cleaned := strings.Trim(word, ".,;:!?|()-\"'")
							if len(cleaned) > 2 {
								customDict[cleaned] = true
							}
						}
					}
				}
			}
		}
		dictRows.Close()
	}
	// Also add the seed domain as a brand word
	job, jobErr := e.db.GetJob(jobID)
	if jobErr == nil {
		var seeds []string
		if err := json.Unmarshal([]byte(job.SeedURLs), &seeds); err == nil {
			for _, seed := range seeds {
				if parsed, err := url.Parse(seed); err == nil {
					host := parsed.Hostname()
					parts := strings.Split(host, ".")
					for _, part := range parts {
						if len(part) > 2 && part != "www" && part != "com" && part != "org" && part != "net" && part != "io" && part != "ai" {
							customDict[part] = true
							customDict[strings.Title(part)] = true
						}
					}
				}
			}
		}
	}
	log.Printf("engine: text quality custom dictionary: %d words", len(customDict))

	log.Printf("engine: running text quality checks on %d pages via LanguageTool", len(pages))
	totalFindings := 0
	checkOpts := textquality.CheckOptions{CustomDict: customDict}

	for _, pg := range pages {
		if ctx.Err() != nil {
			break
		}
		// Re-fetch and extract visible text for this page
		fetchResult, fetchErr := e.fetcher.FetchContext(ctx, pg.url)
		if fetchErr != nil || fetchResult == nil || len(fetchResult.Body) == 0 {
			continue
		}
		parsed, parseErr := parser.ParseHTML(fetchResult.Body, pg.url, fetchResult.ResponseHeaders)
		if parseErr != nil || parsed.ExtractedText == "" {
			continue
		}
		result, err := client.Check(ctx, parsed.ExtractedText, "en-US", checkOpts)
		if err != nil {
			log.Printf("engine: text quality check failed for %s: %v", pg.url, err)
			continue
		}
		if len(result.Matches) == 0 {
			continue
		}

		totalFindings += len(result.Matches)

		// Filter out noisy rules that produce false positives from HTML-extracted text
		noisyRules := map[string]bool{
			"WHITESPACE_RULE":              true,
			"CONSECUTIVE_SPACES":           true,
			"COMMA_PARENTHESIS_WHITESPACE": true,
			"SENTENCE_WHITESPACE":          true,
			"EN_UNPAIRED_BRACKETS":         true,
			"UPPERCASE_SENTENCE_START":     true,
		}

		// Word repeat rules: only filter if the repeat spans a block boundary
		wordRepeatRules := map[string]bool{
			"ENGLISH_WORD_REPEAT_RULE":           true,
			"ENGLISH_WORD_REPEAT_BEGINNING_RULE": true,
			"WORD_REPEAT_RULE":                   true,
			"PHRASE_REPETITION":                  true,
		}

		// Use boundary-marked text to detect cross-component repeats
		boundaryText := parsed.ExtractedTextWithBounds

		// Group by category for cleaner issue creation
		for _, match := range result.Matches {
			if noisyRules[match.RuleID] {
				continue
			}

			// For word repeat rules, check if the repeat spans a block boundary
			if wordRepeatRules[match.RuleID] && len(boundaryText) > 0 {
				// Find the approximate region in the boundary text
				// The boundary text has extra separator chars, so offsets don't align exactly.
				// Instead, check if the flagged sentence context contains a block boundary.
				if match.Offset >= 0 && match.Length > 0 {
					// Search for the repeated word in boundary text near the offset
					end := match.Offset + match.Length
					if end > len(boundaryText) {
						end = len(boundaryText)
					}
					start := match.Offset - 20
					if start < 0 {
						start = 0
					}
					window := boundaryText[start:min(end+20, len(boundaryText))]
					if strings.Contains(window, parser.BlockSeparator) {
						continue // repeat spans different HTML blocks — false positive
					}
				}
			}
			detailsJSON, _ := json.Marshal(map[string]interface{}{
				"message":      match.Message,
				"ruleId":       match.RuleID,
				"category":     match.RuleCategory,
				"context":      match.Context,
				"sentence":     match.Sentence,
				"offset":       match.Offset,
				"length":       match.Length,
				"replacements": match.Replacements,
				"language":     result.Language,
			})
			details := string(detailsJSON)

			severity := "info"
			issueType := "text_quality_style"
			switch {
			case match.RuleCategory == "Possible Typo" || match.ShortMessage == "Spelling mistake":
				issueType = "text_quality_spelling"
				severity = "warning"
			case match.RuleCategory == "Grammar" || match.RuleCategory == "Misc":
				issueType = "text_quality_grammar"
				severity = "warning"
			case match.RuleCategory == "Punctuation":
				issueType = "text_quality_punctuation"
				severity = "info"
			}

			e.db.InsertIssue(storage.IssueInput{
				JobID:       jobID,
				URLID:       &pg.urlID,
				IssueType:   issueType,
				Severity:    severity,
				Scope:       "page_local",
				DetailsJSON: &details,
			})
		}
	}

	log.Printf("engine: text quality checks complete: %d findings across %d pages", totalFindings, len(pages))
}

// browserEnrichPages re-renders pages with Playwright (full scroll) to capture
// lazy-loaded content. Only targets pages that look incomplete: JS-suspect,
// thin content, or built with known JS frameworks.
func (e *Engine) browserEnrichPages(ctx context.Context, jobID string) {
	rows, err := e.db.Query(`
		SELECT p.url_id, u.normalized_url, p.word_count, p.js_suspect
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		WHERE p.job_id = ?
		  AND (p.js_suspect = 1 OR p.word_count < ? OR p.word_count IS NULL)
	`, jobID, e.config.ThinContentThreshold*3)
	if err != nil {
		log.Printf("engine: browser enrich: query failed: %v", err)
		return
	}
	defer rows.Close()

	type pageInfo struct {
		urlID     int64
		url       string
		wordCount int
		jsSuspect bool
	}
	var pages []pageInfo
	for rows.Next() {
		var pi pageInfo
		var jsSuspect int
		if err := rows.Scan(&pi.urlID, &pi.url, &pi.wordCount, &jsSuspect); err != nil {
			continue
		}
		pi.jsSuspect = jsSuspect == 1
		pages = append(pages, pi)
	}
	if len(pages) == 0 {
		return
	}

	log.Printf("engine: browser enrich: re-rendering %d pages with full scroll", len(pages))
	enriched := 0

	for _, pg := range pages {
		if ctx.Err() != nil {
			break
		}
		// Use content-only render (no menu clicks) to preserve page's own content
		pwResult, pwErr := renderer.RenderPageContentOnly(ctx, pg.url)
		if pwErr != nil {
			continue
		}
		page, parseErr := parser.ParseHTML([]byte(pwResult.HTML), pg.url, http.Header{})
		if parseErr != nil {
			continue
		}
		if page.ExtractedWordCount <= pg.wordCount {
			continue // static version already had equal or more content
		}

		enriched++
		e.db.Exec(`
			UPDATE pages SET
				word_count = ?,
				main_content_word_count = ?,
				content_hash = ?,
				h1_json = ?,
				h2_json = ?,
				images_json = ?
			WHERE job_id = ? AND url_id = ?`,
			page.ExtractedWordCount, page.MainContentWordCount,
			page.ContentHash,
			marshalStringSlice(page.Headings.H1),
			marshalStringSlice(page.Headings.H2),
			marshalImages(page.Images),
			jobID, pg.urlID,
		)

		// Register newly discovered images as assets + asset_references
		// so they get HEAD-checked in the next phase
		for _, img := range page.Images {
			if img.Src == "" {
				continue
			}
			parsed, parseErr := url.Parse(img.Src)
			if parseErr != nil {
				continue
			}
			imgHost := parsed.Hostname()
			imgURLID, upsertErr := e.db.UpsertURL(jobID, img.Src, imgHost, "discovered", false, "asset")
			if upsertErr != nil {
				continue
			}
			// Insert asset (ignore if already exists)
			e.db.Exec(`
				INSERT OR IGNORE INTO assets (job_id, url_id)
				VALUES (?, ?)`,
				jobID, imgURLID,
			)
			// Insert asset_reference (ignore if already exists)
			e.db.Exec(`
				INSERT OR IGNORE INTO asset_references (job_id, asset_url_id, source_page_url_id, reference_type)
				VALUES (?, ?, ?, 'img_src')`,
				jobID, imgURLID, pg.urlID,
			)
		}
	}

	log.Printf("engine: browser enrich: updated %d/%d pages with richer content", enriched, len(pages))
}

func marshalStringSlice(items []string) string {
	raw, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func marshalImages(images []parser.DiscoveredImage) string {
	raw, err := json.Marshal(images)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func marshalJSONLDBlocks(blocks []parser.JSONLDBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return ""
	}
	return string(raw)
}

func hasMalformedJSONLD(blocks []parser.JSONLDBlock) bool {
	for _, b := range blocks {
		if b.Malformed {
			return true
		}
	}
	return false
}

func countImagesWithoutAlt(images []parser.DiscoveredImage) int {
	count := 0
	for _, img := range images {
		if img.AltMissing {
			count++
		}
	}
	return count
}

func countImagesWithEmptyAlt(images []parser.DiscoveredImage) int {
	count := 0
	for _, img := range images {
		if img.AltEmpty {
			count++
		}
	}
	return count
}

// recomputePageDepths recalculates page depth from the final internal link graph,
// using the shortest path from the job's seed URLs. This avoids stale depths when
// a page is first discovered via a longer path and a shorter path is found later.
func (e *Engine) recomputePageDepths(jobID string) error {
	job, err := e.db.GetJob(jobID)
	if err != nil {
		return fmt.Errorf("get job: %w", err)
	}

	var seedURLs []string
	if err := json.Unmarshal([]byte(job.SeedURLs), &seedURLs); err != nil {
		return fmt.Errorf("parse seed urls: %w", err)
	}

	pageRows, err := e.db.Query(`
		SELECT u.id, u.normalized_url, p.depth
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		WHERE p.job_id = ?
	`, jobID)
	if err != nil {
		return fmt.Errorf("query pages: %w", err)
	}
	defer pageRows.Close()

	type pageNode struct {
		urlID int64
		depth int
	}
	pages := map[string]pageNode{}
	for pageRows.Next() {
		var urlID int64
		var normalizedURL string
		var depth int
		if err := pageRows.Scan(&urlID, &normalizedURL, &depth); err != nil {
			return fmt.Errorf("scan page: %w", err)
		}
		pages[normalizedURL] = pageNode{urlID: urlID, depth: depth}
	}
	if err := pageRows.Err(); err != nil {
		return fmt.Errorf("iterate pages: %w", err)
	}
	if len(pages) == 0 {
		return nil
	}

	adj := map[string][]string{}
	edgeRows, err := e.db.Query(`
		SELECT su.normalized_url, tu.normalized_url
		FROM edges e
		JOIN urls su ON su.id = e.source_url_id AND su.job_id = e.job_id
		JOIN urls tu ON tu.normalized_url = e.declared_target_url AND tu.job_id = e.job_id
		JOIN pages sp ON sp.url_id = su.id AND sp.job_id = su.job_id
		JOIN pages tp ON tp.url_id = tu.id AND tp.job_id = tu.job_id
		WHERE e.job_id = ?
		  AND e.is_internal = 1
		  AND e.relation_type = 'link'
	`, jobID)
	if err != nil {
		return fmt.Errorf("query edges: %w", err)
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var sourceURL, targetURL string
		if err := edgeRows.Scan(&sourceURL, &targetURL); err != nil {
			return fmt.Errorf("scan edge: %w", err)
		}
		adj[sourceURL] = append(adj[sourceURL], targetURL)
	}
	if err := edgeRows.Err(); err != nil {
		return fmt.Errorf("iterate edges: %w", err)
	}

	depths := map[string]int{}
	queue := make([]string, 0, len(seedURLs))
	for _, seed := range seedURLs {
		normalizedSeed, normErr := urlutil.Normalize(seed)
		if normErr != nil {
			continue
		}
		if _, ok := pages[normalizedSeed]; !ok {
			continue
		}
		if _, seen := depths[normalizedSeed]; seen {
			continue
		}
		depths[normalizedSeed] = 0
		queue = append(queue, normalizedSeed)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		currentDepth := depths[current]
		for _, next := range adj[current] {
			if _, ok := pages[next]; !ok {
				continue
			}
			if _, seen := depths[next]; seen {
				continue
			}
			depths[next] = currentDepth + 1
			queue = append(queue, next)
		}
	}

	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("begin depth tx: %w", err)
	}
	defer tx.Rollback()

	for normalizedURL, page := range pages {
		depth := page.depth
		if recomputedDepth, ok := depths[normalizedURL]; ok {
			depth = recomputedDepth
		}
		if _, err := tx.Exec(`UPDATE pages SET depth = ? WHERE job_id = ? AND url_id = ?`, depth, jobID, page.urlID); err != nil {
			return fmt.Errorf("update depth for %s: %w", normalizedURL, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit depth tx: %w", err)
	}
	return nil
}
