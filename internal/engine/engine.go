// Package engine orchestrates the SEO crawl pipeline.
package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	runMu        sync.Mutex
	// scopeCheckerExplicit is true when the caller supplied a fixed checker in
	// EngineConfig. Otherwise the checker is derived fresh from each job's seed.
	// The HTTP server reuses one Engine across many jobs, so caching the first
	// derived checker leaks scope between different sites.
	scopeCheckerExplicit bool
	ssrfGuard            *ssrf.Guard
	config               *config.Config
	renderer             *renderer.Pool

	// robotsRules caches parsed robots.txt per host during a crawl.
	robotsRules   map[string]*robots.RobotsFile
	robotsRulesMu sync.RWMutex
}

// New creates a new crawl engine.
func New(cfg EngineConfig) *Engine {
	return &Engine{
		db:                   cfg.DB,
		fetcher:              cfg.Fetcher,
		rateLimiter:          cfg.RateLimiter,
		scopeChecker:         cfg.ScopeChecker,
		scopeCheckerExplicit: cfg.ScopeChecker != nil,
		ssrfGuard:            cfg.SSRFGuard,
		config:               cfg.Config,
		renderer:             cfg.Renderer,
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

// crawlCounters holds the live counters for an in-flight crawl. They are
// mutated concurrently by the pipeline workers and read periodically for
// status reporting and the final job row update.
type crawlCounters struct {
	pagesCrawled   atomic.Int64
	urlsDiscovered atomic.Int64
	issuesFound    atomic.Int64
}

// queryVariantsTracker counts UNIQUE query strings observed per URL path
// and remembers which paths have already produced a crawl_trap_suspected
// issue, so the trap heuristic emits at most one issue per path no
// matter how many trap-shaped links the crawl finds.
//
// The previous implementation incremented a counter per discovered edge
// without deduping queries and emitted an issue on every discovery past
// the threshold, which caused thousands of duplicate issues on sites
// with one or two faceted-search paths linked from many pages.
type queryVariantsTracker struct {
	mu      sync.Mutex
	seen    map[string]map[string]struct{} // path -> set of unique RawQuery values
	flagged map[string]bool                // path -> issue already emitted?
}

func newQueryVariantsTracker() *queryVariantsTracker {
	return &queryVariantsTracker{
		seen:    map[string]map[string]struct{}{},
		flagged: map[string]bool{},
	}
}

// observe records a (path, query) pair and returns:
//   - count: the number of unique queries seen for this path so far,
//   - shouldEmitIssue: true iff this is the first call that pushes count
//     strictly past `threshold` AND no issue has been emitted yet for
//     this path.
//
// Callers that get shouldEmitIssue=true should write exactly one
// crawl_trap_suspected issue and then continue skipping further
// discoveries for the same path (count remains > threshold for those).
func (t *queryVariantsTracker) observe(path, query string, threshold int) (count int, shouldEmitIssue bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	bucket, ok := t.seen[path]
	if !ok {
		bucket = map[string]struct{}{}
		t.seen[path] = bucket
	}
	bucket[query] = struct{}{}
	count = len(bucket)

	if count > threshold && !t.flagged[path] {
		t.flagged[path] = true
		shouldEmitIssue = true
	}
	return count, shouldEmitIssue
}

func recoverWorker(cancel context.CancelCauseFunc, name string) {
	if r := recover(); r != nil {
		if rerr, ok := r.(error); ok {
			cancel(fmt.Errorf("%s panic: %w", name, rerr))
			return
		}
		cancel(fmt.Errorf("%s panic: %v", name, r))
	}
}

// validateRenderTarget re-checks a URL against the SSRF guard before passing
// it to a browser renderer. Browser processes (Chromedp / Playwright) do not
// share the fetcher's DialContext, so the URL must be re-validated here:
// it may have been written to the DB without going through the fetcher
// guard, and DNS may have changed since the initial fetch (rebinding).
// If no guard is configured, this is a no-op.
func (e *Engine) validateRenderTarget(rawURL string) error {
	if e.ssrfGuard == nil {
		return nil
	}
	if err := e.ssrfGuard.ValidateURL(rawURL); err != nil {
		return err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ssrf: invalid render URL: %w", err)
	}
	return e.ssrfGuard.ValidateHost(parsed.Hostname())
}

func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (e *Engine) cancelJob(jobID string, err error) error {
	if err == nil {
		err = context.Canceled
	}
	msg := err.Error()
	if updateErr := e.db.UpdateJobFinished(jobID, "cancelled", &msg); updateErr != nil {
		return fmt.Errorf("cancelling job %s: %w", jobID, updateErr)
	}
	return context.Canceled
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
		slog.Error("engine: emitPhase marshal failed", "err", err, "job_id", jobID, "phase", phase)
		return
	}
	details := string(payload)
	if _, err := e.db.InsertEvent(jobID, "phase", &details, nil); err != nil {
		slog.Error("engine: emitPhase insert failed", "err", err, "job_id", jobID, "phase", phase)
	}
	slog.Info("engine: phase", "phase", phase, "message", message, "job_id", jobID)
}

// RunCrawl executes a full crawl job. Blocks until complete or cancelled.
//
// Pipeline phases: startJob -> seedFrontier -> onboardSeedHosts ->
// runCrawlPipeline -> runPostCrawlPhases -> finalizeJob. Each phase is a
// dedicated method to keep this top-level orchestrator readable and to
// make the phases independently testable.
func (e *Engine) RunCrawl(ctx context.Context, jobID string) error {
	// Engine currently owns crawl-scoped mutable fields (derived scope checker,
	// robots cache, and rate limiter reset state). Serialize runs on one Engine
	// instance until those fields are moved into explicit per-run state.
	e.runMu.Lock()
	defer e.runMu.Unlock()

	job, err := e.startJob(ctx, jobID)
	if err != nil {
		return err
	}

	// Clear cross-job state in shared services before this job populates them.
	// (e.robotsRules is reset inside onboardSeedHosts; the rate limiter doesn't
	// reset itself.)
	e.rateLimiter.Reset()

	q, seeds, err := e.seedFrontier(jobID, job)
	if err != nil {
		return err
	}

	e.onboardSeedHosts(ctx, jobID, seeds, q)

	counters := &crawlCounters{}
	counters.urlsDiscovered.Store(int64(q.Len()))

	// WithCancelCause so any fatal worker error (e.g. persister) unwinds
	// the whole pipeline AND post-crawl audit goroutines.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	effectiveMaxPages := e.effectiveMaxPages(job)
	effectiveMaxDepth := e.effectiveMaxDepth(job)
	effectiveRenderMode := e.effectiveRenderMode(job)
	completionErr := e.runCrawlPipeline(ctx, cancel, jobID, q, counters, effectiveMaxPages, effectiveMaxDepth)

	if completionErr == nil {
		e.runPostCrawlPhases(ctx, jobID, counters, cancel, effectiveRenderMode, effectiveMaxPages, effectiveMaxDepth)
	}

	return e.finalizeJob(ctx, jobID, completionErr, counters)
}

// startJob loads the job row, validates that it is in `queued` state,
// transitions it to `running`, and runs cheap startup chores
// (purging expired analyze jobs). Returns the loaded job for downstream
// phases. If the job has already been cancelled or the context is done,
// returns context.Canceled after writing the cancelled status to DB.
func (e *Engine) startJob(ctx context.Context, jobID string) (*storage.CrawlJob, error) {
	job, err := e.db.GetJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("loading job: %w", err)
	}
	if job.Status == "cancelled" || job.Status == "cancelling" {
		return nil, e.cancelJob(jobID, context.Canceled)
	}
	alreadyStarted := job.Status == "running"
	if job.Status != "queued" && !alreadyStarted {
		return nil, fmt.Errorf("job %s has status %q, expected queued or running", jobID, job.Status)
	}
	if err := ctx.Err(); err != nil {
		return nil, e.cancelJob(jobID, err)
	}
	if !alreadyStarted {
		if err := e.db.UpdateJobStarted(jobID); err != nil {
			current, getErr := e.db.GetJob(jobID)
			if getErr == nil && (current.Status == "cancelled" || current.Status == "cancelling" || ctx.Err() != nil) {
				if ctx.Err() != nil {
					return nil, e.cancelJob(jobID, ctx.Err())
				}
				return nil, e.cancelJob(jobID, context.Canceled)
			}
			return nil, fmt.Errorf("starting job: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, e.cancelJob(jobID, err)
	}

	e.db.PurgeExpiredAnalyzeJobs()
	return job, nil
}

// seedFrontier parses job.SeedURLs, normalizes/upserts each, pushes valid
// seeds to a fresh frontier queue, and ensures e.scopeChecker is set. The
// returned []string is the original seed list (used by onboarding for
// robots.txt and sitemap discovery, which must run per host).
func (e *Engine) seedFrontier(jobID string, job *storage.CrawlJob) (*frontier.Queue, []string, error) {
	var seeds []string
	if err := json.Unmarshal([]byte(job.SeedURLs), &seeds); err != nil {
		return nil, nil, e.failJob(jobID, fmt.Errorf("parsing seed URLs: %w", err))
	}

	q := frontier.New()
	for _, seedURL := range seeds {
		normalized, err := urlutil.Normalize(seedURL)
		if err != nil {
			slog.Warn("engine: skipping invalid seed URL", "url", seedURL, "err", err)
			continue
		}
		parsed, err := url.Parse(normalized)
		if err != nil {
			continue
		}
		host := parsed.Hostname()
		urlID, err := e.db.UpsertURL(jobID, normalized, host, "queued", true, "seed")
		if err != nil {
			return nil, nil, e.failJob(jobID, fmt.Errorf("upserting seed URL: %w", err))
		}
		q.Push(frontier.Item{
			URLID:         urlID,
			NormalizedURL: normalized,
			Host:          host,
			Depth:         0,
		})
	}

	if q.Len() == 0 {
		return nil, nil, e.failJob(jobID, fmt.Errorf("no valid seed URLs"))
	}

	if err := e.ensureScopeChecker(job, q); err != nil {
		return nil, nil, e.failJob(jobID, err)
	}
	return q, seeds, nil
}

// ensureScopeChecker constructs e.scopeChecker from the first frontier
// seed. Derived checkers are rebuilt for every job because the HTTP server
// reuses one Engine across many unrelated sites. Only a checker explicitly
// supplied via EngineConfig is kept.
func (e *Engine) ensureScopeChecker(job *storage.CrawlJob, q *frontier.Queue) error {
	if e.scopeCheckerExplicit && e.scopeChecker != nil {
		return nil
	}
	first := q.Peek()
	scopeMode := "registrable_domain"
	var allowedHosts []string
	if e.config != nil {
		scopeMode = string(e.config.ScopeMode)
		allowedHosts = e.config.AllowedHosts
	}
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
		return fmt.Errorf("creating scope checker: %w", err)
	}
	e.scopeChecker = sc
	return nil
}

func (e *Engine) effectiveMaxPages(job *storage.CrawlJob) int {
	maxPages := e.config.MaxPages
	var jobCfg struct {
		MaxPages int `json:"maxPages"`
	}
	if err := json.Unmarshal([]byte(job.ConfigJSON), &jobCfg); err == nil && jobCfg.MaxPages > 0 {
		maxPages = jobCfg.MaxPages
	}
	return maxPages
}

func (e *Engine) effectiveMaxDepth(job *storage.CrawlJob) int {
	maxDepth := e.config.MaxDepth
	var jobCfg struct {
		MaxDepth int `json:"maxDepth"`
	}
	if err := json.Unmarshal([]byte(job.ConfigJSON), &jobCfg); err == nil && jobCfg.MaxDepth > 0 {
		maxDepth = jobCfg.MaxDepth
	}
	return maxDepth
}

func (e *Engine) effectiveRenderMode(job *storage.CrawlJob) config.RenderMode {
	renderMode := e.config.RenderMode
	var jobCfg struct {
		RenderMode string `json:"renderMode"`
	}
	if err := json.Unmarshal([]byte(job.ConfigJSON), &jobCfg); err == nil && jobCfg.RenderMode != "" {
		renderMode = config.RenderMode(jobCfg.RenderMode)
	}
	return renderMode
}

// onboardSeedHosts performs robots.txt + sitemap discovery for each
// distinct seed host, populates e.robotsRules and per-host crawl-delay
// in the rate limiter, and pushes any sitemap-discovered URLs onto the
// frontier (subject to scope and robots checks).
func (e *Engine) onboardSeedHosts(ctx context.Context, jobID string, seeds []string, q *frontier.Queue) {
	e.robotsRules = map[string]*robots.RobotsFile{}

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
		hostWithPort := parsed.Host
		hostOnly := parsed.Hostname()
		if seenHosts[hostWithPort] {
			continue
		}
		seenHosts[hostWithPort] = true

		info, onboardErr := onboarder.OnboardHost(ctx, jobID, hostWithPort, parsed.Scheme)
		if onboardErr != nil {
			slog.Warn("engine: onboarding host failed", "host", hostWithPort, "err", onboardErr)
			continue
		}

		// Cache robots rules for fetch-time checking (keyed by hostname without port).
		if info.RobotsFile != nil {
			e.robotsRulesMu.Lock()
			e.robotsRules[hostOnly] = info.RobotsFile
			e.robotsRulesMu.Unlock()
		}

		if info.CrawlDelay > 0 {
			e.rateLimiter.SetCrawlDelay(hostOnly, info.CrawlDelay)
			slog.Info("engine: crawl-delay applied", "host", hostOnly, "delay", info.CrawlDelay)
		}

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

			if e.scopeChecker != nil && !e.scopeChecker.IsInScope(normalized) {
				continue
			}
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
					Depth:         1,
				})
			}
		}

		eventDetails := fmt.Sprintf(`{"host":%q,"sitemapEntries":%d,"crawlDelay":%q}`,
			hostWithPort, len(info.SitemapEntries), info.CrawlDelay.String())
		e.db.InsertEvent(jobID, "host_onboarded", &eventDetails, nil)
	}
}

// runCrawlPipeline runs the fetch -> parse -> persist worker pools and the
// dispatcher loop that drains the frontier queue. Returns the completion
// error (nil on clean drain, or the cause set on the cancel context if a
// worker fatally fails or the job is cancelled).
//
// The pipeline owns a status watcher (polls the DB for cancelling state)
// and a periodic counter flusher; both shut down on ctx.Done.
func (e *Engine) runCrawlPipeline(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	jobID string,
	q *frontier.Queue,
	counters *crawlCounters,
	maxPages int,
	maxDepth int,
) error {
	// Crawl-trap detection: tracks unique query strings per path so the
	// trap heuristic counts variants (not raw discoveries) and emits at
	// most one issue per path.
	queryVariants := newQueryVariantsTracker()

	fetchQueue := make(chan frontier.Item, 64)
	fetchResults := make(chan fetchResult, 64)
	persistQueue := make(chan persistItem, 128)

	var fetchSeq atomic.Int64
	var lastFetchSeq int64
	if err := e.db.QueryRow(`SELECT COALESCE(MAX(fetch_seq), 0) FROM fetches WHERE job_id = ?`, jobID).Scan(&lastFetchSeq); err != nil {
		slog.Warn("engine: failed to load last fetch sequence", "err", err, "job_id", jobID)
	} else {
		fetchSeq.Store(lastFetchSeq)
	}

	concurrency := e.config.GlobalConcurrency
	if concurrency < 1 {
		concurrency = 8
	}
	parserCount := 4

	// Status watcher: polls DB for `cancelling` so the user can cancel
	// from the API while the pipeline is running.
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

	// Periodic counter flush so polling clients see live progress.
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
				if err := e.db.UpdateJobCounters(jobID, int(counters.pagesCrawled.Load()), int(counters.urlsDiscovered.Load()), int(counters.issuesFound.Load())); err != nil {
					slog.Error("engine: counter flush failed", "err", err, "job_id", jobID)
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

	// Persister (1 goroutine) — serialized writes to SQLite.
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

	// Parser pool.
	var parserWg sync.WaitGroup
	for range parserCount {
		parserWg.Add(1)
		go func() {
			defer recoverWorker(cancel, "parser")
			defer parserWg.Done()
			for fr := range fetchResults {
				pr := e.processParseResult(ctx, jobID, fr, q, &counters.pagesCrawled, &counters.urlsDiscovered, queryVariants, maxPages, maxDepth)
				counters.issuesFound.Add(int64(len(pr.issues)))
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

	// Fetcher pool.
	var fetcherWg sync.WaitGroup
	for range concurrency {
		fetcherWg.Add(1)
		go func() {
			defer recoverWorker(cancel, "fetcher")
			defer fetcherWg.Done()
			for item := range fetchQueue {
				if !e.scopeChecker.IsInScope(item.NormalizedURL) {
					inFlight.Add(-1)
					continue
				}

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

				if err := e.rateLimiter.AcquireContext(ctx, item.Host); err != nil {
					inFlight.Add(-1)
					return
				}

				seq := int(fetchSeq.Add(1))
				result, fetchErr := e.fetcher.FetchContext(ctx, item.NormalizedURL)
				e.rateLimiter.Release(item.Host)

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

	// Dispatcher: drains the frontier into fetchQueue. inFlight is
	// incremented HERE (before send) and decremented in the persister
	// (after persist), eliminating the race between pop and tracking.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	var completionErr error
loop:
	for {
		for {
			if maxPages > 0 && int(counters.pagesCrawled.Load()+inFlight.Load()) >= maxPages {
				break
			}
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

		// Check completion: nothing in queue and nothing in flight. If the max-page
		// scheduling cap is reached, do not wait for leftover frontier URLs.
		maxPagesReached := maxPages > 0 && int(counters.pagesCrawled.Load()+inFlight.Load()) >= maxPages
		if (q.Len() == 0 || maxPagesReached) && inFlight.Load() == 0 {
			break loop
		}

		select {
		case <-ctx.Done():
			completionErr = context.Cause(ctx)
			break loop
		case <-ticker.C:
		}
	}

	// Shutdown pipeline in order.
	close(fetchQueue)
	fetcherWg.Wait()

	close(fetchResults)
	parserWg.Wait()

	close(persistQueue)
	persisterWg.Wait()

	return completionErr
}

// runPostCrawlPhases runs analyses that happen after the fetch pipeline
// completes successfully: sitemap-gap browser escalation, lazy-content
// re-render, asset HEAD checks, PSI+Axe audits, markdown negotiation,
// text quality checks, edge count recomputation, global issue detection,
// and materialization. Skipped entirely if the crawl pipeline failed.
//
// `cancel` is the pipeline's CancelCauseFunc; passed so panic recovers in
// goroutines spawned here can unwind the whole crawl.
func (e *Engine) runPostCrawlPhases(ctx context.Context, jobID string, counters *crawlCounters, cancel context.CancelCauseFunc, renderMode config.RenderMode, maxPages int, maxDepth int) {
	// Sitemap gap browser escalation (hybrid/browser mode).
	if renderMode != config.RenderModeStatic {
		e.emitPhase(jobID, "sitemap_gap", "checking sitemap URLs missing from crawl (JS render)")
		if escalated := e.sitemapGapEscalation(ctx, jobID); escalated > 0 {
			slog.Info("engine: sitemap gap escalation discovered new URLs", "count", escalated, "job_id", jobID)
		}
		if q := e.queueBrowserDiscoveredLinkURLs(jobID, maxDepth); q.Len() > 0 {
			e.emitPhase(jobID, "browser_discovered_crawl", fmt.Sprintf("crawling %d browser-discovered internal URLs", q.Len()))
			if err := e.runCrawlPipeline(ctx, cancel, jobID, q, counters, maxPages, maxDepth); err != nil {
				cancel(err)
				return
			}
		}
	}

	// Browser re-render to capture lazy-loaded content.
	if renderMode != config.RenderModeStatic && renderer.IsPlaywrightAvailable() {
		e.browserEnrichPages(ctx, jobID)
	}

	// HEAD-check discovered image/script/stylesheet assets.
	e.headCheckAssets(ctx, jobID)

	// Performance + Accessibility audits (parallel).
	var auditWg sync.WaitGroup
	slog.Info("engine: PSI API key configured", "configured", e.config != nil && e.config.PSIAPIKey != "")
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

	// Markdown content negotiation.
	e.emitPhase(jobID, "markdown_negotiation", "checking which pages support text/markdown")
	e.checkMarkdownNegotiation(ctx, jobID)

	// Text quality via LanguageTool.
	if e.config.LanguageToolURL != "" {
		e.runTextQualityChecks(ctx, jobID)
	}

	// Recalculate inbound/outbound edge counts and shortest-path depths.
	if err := e.recalculatePageLinkCounts(jobID); err != nil {
		slog.Warn("engine: recalculate page link counts failed", "err", err, "job_id", jobID)
	}
	if depthErr := e.recomputePageDepths(jobID); depthErr != nil {
		slog.Error("engine: page depth recomputation failed", "err", depthErr, "job_id", jobID)
	}
	slog.Info("engine: recalculated edge counts", "job_id", jobID)

	// Global issue detection + materialization.
	e.emitPhase(jobID, "global_issues", "detecting site-wide issues (duplicates, clusters, gaps)")
	globalCfg := issues.DefaultGlobalConfig()
	if e.config.ThinContentThreshold > 0 {
		globalCfg.ThinContentThreshold = e.config.ThinContentThreshold
	}
	if e.config.DeepPageThreshold > 0 {
		globalCfg.DeepPageThreshold = e.config.DeepPageThreshold
	}
	globalCount, globalErr := issues.DetectGlobalIssues(e.db, jobID, globalCfg)
	if globalErr != nil {
		slog.Error("engine: global issue detection failed", "err", globalErr, "job_id", jobID)
	} else {
		counters.issuesFound.Add(int64(globalCount))
		e.emitPhase(jobID, "global_issues_done", fmt.Sprintf("%d global issues detected", globalCount))
	}

	e.emitPhase(jobID, "materializing", "writing report rollups")
	if matErr := materialize.Materialize(e.db, jobID); matErr != nil {
		slog.Error("engine: materialization failed", "err", matErr, "job_id", jobID)
	}

	// Re-sync issuesFound from the actual issues table so the job row
	// reflects everything written by post-crawl phases (text quality, audits, etc.)
	var actual int
	if scanErr := e.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE job_id = ?`, jobID).Scan(&actual); scanErr == nil {
		counters.issuesFound.Store(int64(actual))
	}
}

// finalizeJob writes the final counters to the job row and sets the
// terminal status (completed/failed/cancelled) based on completionErr.
func (e *Engine) finalizeJob(ctx context.Context, jobID string, completionErr error, counters *crawlCounters) error {
	e.db.UpdateJobCounters(
		jobID,
		int(counters.pagesCrawled.Load()),
		int(counters.urlsDiscovered.Load()),
		int(counters.issuesFound.Load()),
	)

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
	queryVariants *queryVariantsTracker,
	maxPages int,
	maxDepth int,
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
	if maxPages > 0 && int(newCount) > maxPages {
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
		HasFavicon:                   hasFaviconAsset(page.Assets),
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
		PageURL:                      fr.result.FinalURL,
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
		if newDepth > maxDepth {
			continue
		}

		// MaxPages check
		if maxPages > 0 && int(pagesCrawled.Load()) >= maxPages {
			continue
		}

		// Crawl trap: repeated path segments
		if urlutil.HasRepeatedPathSegments(normalized) {
			continue
		}

		// Crawl trap: query variant limit. Counts UNIQUE query strings
		// per path (not edge discoveries) and emits at most one issue
		// per path. Once a path is flagged, subsequent variant URLs are
		// silently skipped from the frontier.
		parsed, parseErr := url.Parse(normalized)
		if parseErr != nil {
			continue
		}
		pathKey := parsed.Path
		if parsed.RawQuery != "" {
			count, shouldEmit := queryVariants.observe(pathKey, parsed.RawQuery, e.config.MaxQueryVariantsPerPath)
			if count > e.config.MaxQueryVariantsPerPath {
				if shouldEmit {
					detailsJSON := fmt.Sprintf(`{"path":%q,"uniqueQueryVariants":%d,"limit":%d,"sampleUrl":%q}`,
						pathKey, count, e.config.MaxQueryVariantsPerPath, normalized)
					e.db.InsertIssue(storage.IssueInput{
						JobID:       jobID,
						URLID:       nil,
						IssueType:   "crawl_trap_suspected",
						Severity:    "info",
						Scope:       "global",
						DetailsJSON: &detailsJSON,
					})
				}
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
		slog.Error("engine: sitemap gap: query sitemap entries failed", "err", err, "job_id", jobID)
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
		slog.Error("engine: sitemap gap: query static edges failed", "err", err, "job_id", jobID)
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

	slog.Info("engine: sitemap gap detected", "orphan_count", len(gap), "job_id", jobID)

	// 4. Check renderer availability
	if e.renderer == nil {
		slog.Warn("engine: sitemap gap detected but no renderer available, skipping escalation", "job_id", jobID)
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
		slog.Error("engine: sitemap gap: query key pages failed", "err", err, "job_id", jobID)
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

	e.emitPhase(jobID, "sitemap_gap_progress", fmt.Sprintf("found %d sitemap URLs missing from crawl, re-rendering %d key pages", len(gap), len(keyPages)))

	for i, kp := range keyPages {
		if ctx.Err() != nil {
			break
		}

		e.emitPhase(jobID, "sitemap_gap_progress", fmt.Sprintf("re-rendering page %d/%d (%d new URLs found so far)", i+1, len(keyPages), newURLsDiscovered))

		if err := e.validateRenderTarget(kp.url); err != nil {
			slog.Warn("engine: sitemap gap: ssrf rejected", "url", kp.url, "err", err)
			continue
		}

		// Try Playwright first (better menu discovery via real click handlers),
		// fall back to chromedp if Playwright is unavailable or fails.
		var renderHTML string
		var renderFinalURL string

		var playwrightLinks []string
		if renderer.IsPlaywrightAvailable() {
			pwResult, pwErr := renderer.RenderWithPlaywright(ctx, kp.url)
			if pwErr != nil {
				slog.Warn("engine: sitemap gap: playwright render failed, falling back to chromedp", "url", kp.url, "err", pwErr)
			} else {
				renderHTML = pwResult.HTML
				renderFinalURL = pwResult.FinalURL
				playwrightLinks = pwResult.Links // Links collected incrementally during menu clicks
			}
		}

		// Chromedp fallback
		if renderHTML == "" {
			renderResult, renderErr := e.renderer.RenderWithOptions(ctx, kp.url, renderer.RenderOptions{
				DiscoverMenus: true,
			})
			if renderErr != nil {
				slog.Warn("engine: sitemap gap: render failed", "url", kp.url, "err", renderErr)
				continue
			}
			renderHTML = renderResult.HTML
			renderFinalURL = renderResult.FinalURL
		}
		pagesReRendered++

		// Parse the rendered HTML (includes lazy-loaded content after full scroll)
		page, parseErr := parser.ParseHTML([]byte(renderHTML), renderFinalURL, http.Header{})
		if parseErr != nil {
			slog.Warn("engine: sitemap gap: parse failed for rendered", "url", kp.url, "err", parseErr)
			continue
		}

		// Update the page record if browser rendering found more content (lazy loading)
		if page.ExtractedWordCount > 0 {
			if _, err := e.db.Exec(`
				UPDATE pages SET
					word_count = MAX(COALESCE(word_count, 0), ?),
					main_content_word_count = MAX(COALESCE(main_content_word_count, 0), ?),
					content_hash = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE content_hash END,
					text_preview = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE text_preview END,
					h1_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h1_json END,
					h2_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h2_json END,
					h3_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h3_json END,
					h4_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h4_json END,
					h5_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h5_json END,
					h6_json = CASE WHEN ? > COALESCE(word_count, 0) THEN ? ELSE h6_json END,
					images_json = CASE WHEN ? > COALESCE((SELECT COUNT(*) FROM json_each(images_json)), 0) THEN ? ELSE images_json END
				WHERE job_id = ? AND url_id = ?`,
				page.ExtractedWordCount, page.MainContentWordCount,
				page.ExtractedWordCount, page.ContentHash,
				page.ExtractedWordCount, limitTextPreview(page.ExtractedText),
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H1),
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H2),
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H3),
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H4),
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H5),
				page.ExtractedWordCount, marshalStringSlice(page.Headings.H6),
				len(page.Images), marshalImages(page.Images),
				jobID, kp.urlID,
			); err != nil {
				slog.Warn("engine: sitemap gap: update page failed", "url", kp.url, "err", err)
			} else if err := e.removeInvalidatedBrowserIssues(jobID, kp.urlID, page); err != nil {
				slog.Warn("engine: sitemap gap: cleanup stale page issues failed", "url", kp.url, "err", err)
			}
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
				slog.Info("engine: sitemap gap: browser discovered gap URL", "discovered_url", norm, "via_url", kp.url)
			}
		}

		// Also check Playwright-collected links directly (covers menus that close after click)
		for _, pwLink := range playwrightLinks {
			if hasURLFragment(pwLink) {
				continue
			}
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
				slog.Info("engine: sitemap gap: playwright link discovered gap URL", "discovered_url", norm, "via_url", kp.url)
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

func hasURLFragment(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && parsed.Fragment != ""
}

func (e *Engine) queueBrowserDiscoveredLinkURLs(jobID string, maxDepth int) *frontier.Queue {
	q := frontier.New()
	rows, err := e.db.Query(`
		SELECT u.id, u.normalized_url, u.host, MIN(COALESCE(p.depth, 0) + 1) AS depth
		FROM edges e
		JOIN urls u ON u.id = e.normalized_target_url_id AND u.job_id = e.job_id
		LEFT JOIN pages p ON p.job_id = e.job_id AND p.url_id = e.source_url_id
		WHERE e.job_id = ?
		  AND e.relation_type = 'link'
		  AND e.is_internal = 1
		  AND e.discovery_mode = 'browser'
		  AND u.status IN ('discovered', 'queued')
		  AND NOT EXISTS (
		    SELECT 1 FROM pages crawled
		    WHERE crawled.job_id = e.job_id AND crawled.url_id = u.id
		  )
		GROUP BY u.id, u.normalized_url, u.host
		ORDER BY depth ASC, u.id ASC`, jobID)
	if err != nil {
		slog.Warn("engine: browser-discovered URL query failed", "err", err, "job_id", jobID)
		return q
	}
	defer rows.Close()

	seen := map[int64]bool{}
	for rows.Next() {
		var item frontier.Item
		if err := rows.Scan(&item.URLID, &item.NormalizedURL, &item.Host, &item.Depth); err != nil {
			continue
		}
		if seen[item.URLID] {
			continue
		}
		seen[item.URLID] = true
		if maxDepth > 0 && item.Depth > maxDepth {
			continue
		}
		if err := e.db.UpdateURLStatus(item.URLID, "queued"); err != nil {
			slog.Warn("engine: browser-discovered URL queue status update failed", "url", item.NormalizedURL, "err", err)
			continue
		}
		q.Push(item)
	}
	return q
}

// maxAssetHeadChecks bounds the number of unique asset URLs we HEAD-check
// post-crawl. Higher values stretch crawl wall time on link-heavy sites
// without proportionally improving the report; 2000 covers the long tail
// for typical sites.
const maxAssetHeadChecks = 2000

// persistItem saves a single crawl result to the database inside a single transaction.
// headCheckAssets performs HEAD requests on all discovered asset URLs
// (images, scripts, stylesheets, fonts, media, etc.)
// and stores the results in the assets table. Caps at maxAssetHeadChecks
// unique assets.
func (e *Engine) headCheckAssets(ctx context.Context, jobID string) {
	// Query all distinct asset URLs from asset_references for this job
	rows, err := e.db.Query(
		`SELECT DISTINCT ar.asset_url_id, u.normalized_url
		 FROM asset_references ar
		 JOIN urls u ON u.id = ar.asset_url_id
		 WHERE ar.job_id = ?
		 LIMIT ?`,
		jobID, maxAssetHeadChecks,
	)
	if err != nil {
		slog.Error("engine: query assets for HEAD checking failed", "err", err, "job_id", jobID)
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
		slog.Error("engine: iterating assets failed", "err", err, "job_id", jobID)
	}

	if len(targets) == 0 {
		return
	}

	e.emitPhase(jobID, "asset_checks", fmt.Sprintf("HEAD-checking %d discovered assets", len(targets)))
	slog.Info("engine: head-checking discovered assets", "count", len(targets), "job_id", jobID)

	assetProgressEvery := len(targets) / 10
	if assetProgressEvery < 25 {
		assetProgressEvery = 25
	}
	var processed atomic.Int64

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
				}
				if n := int(processed.Add(1)); n%assetProgressEvery == 0 || n == len(targets) {
					e.emitPhase(jobID, "asset_checks_progress", fmt.Sprintf("checked %d/%d assets", n, len(targets)))
				}
			}
		}()
	}
	wg.Wait()
	slog.Info("engine: head-checking assets complete", "job_id", jobID)
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
		finalNormalized, normErr := urlutil.Normalize(fr.result.FinalURL)
		parsed, parseErr := url.Parse(finalNormalized)
		if normErr == nil && parseErr == nil {
			finalInScope := e.scopeChecker.IsInScope(finalNormalized)
			finalStatus := "fetched"
			if !finalInScope {
				finalStatus = "out_of_scope"
			}
			fid, upsertErr := txUpsertURL(tx, jobID, finalNormalized, parsed.Hostname(), finalStatus, finalInScope, "redirect")
			if upsertErr == nil {
				finalURLID = &fid
			}
		}
	}
	pageURLID := fr.urlID
	if finalURLID != nil {
		pageURLID = *finalURLID
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
	pageInserted := true
	if isHTML && item.page != nil {
		var pageErr error
		pageInserted, pageErr = txInsertPage(ctx, tx, jobID, pageURLID, fetchID, fr.depth, item.page)
		if pageErr != nil {
			return fmt.Errorf("inserting page: %w", pageErr)
		}
		if !pageInserted {
			for _, issue := range item.issues {
				if !isDuplicatePageFetchIssue(issue.IssueType) {
					continue
				}
				details := issue.DetailsJSON
				if _, issueErr := tx.ExecContext(ctx,
					`INSERT INTO issues (job_id, url_id, issue_type, severity, scope, details_json) VALUES (?, ?, ?, ?, ?, ?)`,
					jobID, &pageURLID, issue.IssueType, issue.Severity, issue.Scope, &details,
				); issueErr != nil {
					return fmt.Errorf("inserting duplicate-page fetch issue: %w", issueErr)
				}
			}
			return tx.Commit()
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

		// Do not perform network I/O while holding the SQLite transaction. External
		// canonical/hreflang status checks belong in a post-crawl phase; doing HEAD
		// requests here blocks the single DB connection and makes live activity look
		// stuck while a slow third-party URL times out.
		var targetStatusCode *int

		if _, edgeErr := tx.ExecContext(ctx,
			`INSERT INTO edges (job_id, source_url_id, normalized_target_url_id,
				source_kind, relation_type, rel_flags_json, discovery_mode,
				anchor_text, is_internal, declared_target_url, target_status_code)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			jobID, pageURLID, targetURLID,
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
			jobID, &pageURLID, issue.IssueType, issue.Severity, issue.Scope, &details,
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
			jobID, imgURLID, pageURLID, "img_src",
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
			jobID, assetURLID, pageURLID, asset.refType,
		); refErr != nil {
			// Duplicate references are possible; ignore unique constraint errors
			continue
		}
	}

	return tx.Commit()
}

func isDuplicatePageFetchIssue(issueType string) bool {
	switch issueType {
	case "redirect_chain", "redirect_loop", "redirect_hops_exceeded", "rate_limited":
		return true
	default:
		return false
	}
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
func txInsertPage(ctx context.Context, tx *sql.Tx, jobID string, urlID, fetchID int64, depth int, page *parser.ParseResult) (bool, error) {
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
	textPreview := strPtr(limitTextPreview(page.ExtractedText))

	jsSuspect := 0
	if page.JSSuspect {
		jsSuspect = 1
	}

	result, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO pages (job_id, url_id, fetch_id, depth,
			title, title_length, meta_description, meta_description_length,
			meta_robots, x_robots_tag, indexability_state,
			canonical_url, canonical_is_self, rel_next_url, rel_prev_url,
			hreflang_json,
			h1_json, h2_json, h3_json, h4_json, h5_json, h6_json,
			og_title, og_description, og_image, og_url, og_type,
			twitter_card, twitter_title, twitter_description, twitter_image,
			jsonld_raw, jsonld_types_json,
			images_json, word_count, main_content_word_count,
			content_hash, text_preview, js_suspect)
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
			?, ?, ?)`,
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
		contentHash, textPreview, jsSuspect,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
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
		slog.Error("engine: PSI audit query failed", "err", err, "job_id", jobID)
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

	slog.Info("engine: running PSI audits",
		"pages", len(urls),
		"strategies", len(strategies),
		"total_calls", len(urls)*len(strategies),
		"job_id", jobID)

	totalCalls := len(urls) * len(strategies)
	e.emitPhase(jobID, "psi_audits", fmt.Sprintf("running %d PageSpeed audits (%d pages × %d strategies)", totalCalls, len(urls), len(strategies)))
	psiProgressEvery := totalCalls / 10
	if psiProgressEvery < 5 {
		psiProgressEvery = 5
	}

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
					slog.Warn("engine: PSI audit failed", "url", item.url, "strategy", item.strategy, "err", psiErr)
					continue
				}

				detailsBytes, _ := json.Marshal(result)
				details := string(detailsBytes)
				urlStr := item.url
				e.db.InsertEvent(jobID, "psi_audit", &details, &urlStr)

				mu.Lock()
				audited++
				done := audited + failed
				mu.Unlock()
				if done%psiProgressEvery == 0 || done == totalCalls {
					e.emitPhase(jobID, "psi_progress", fmt.Sprintf("audited %d/%d (%d failed)", done, totalCalls, failed))
				}
			}
		}()
	}
	wg.Wait()

	slog.Info("engine: PSI audits complete", "audited", audited, "failed", failed, "job_id", jobID)
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
		slog.Error("engine: Axe audit query failed", "err", err, "job_id", jobID)
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

	slog.Info("engine: running Axe accessibility audits (batch mode)", "pages", len(urls), "job_id", jobID)
	e.emitPhase(jobID, "axe_audits", fmt.Sprintf("running Axe accessibility audits on %d pages (single Playwright batch)", len(urls)))

	// Run all URLs in a single batch — one browser launch for all pages
	results, batchErr := renderer.RunAxeAuditBatch(ctx, urls)
	if batchErr != nil {
		slog.Error("engine: Axe batch audit failed", "err", batchErr, "job_id", jobID)
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

	slog.Info("engine: Axe accessibility audits complete", "audited", audited, "job_id", jobID)
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
		slog.Error("engine: markdown negotiation query failed", "err", err, "job_id", jobID)
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

	slog.Info("engine: checking markdown content negotiation", "pages", len(urls), "job_id", jobID)

	client := &http.Client{Timeout: 5 * time.Second} // shorter timeout: most servers respond fast or 404
	if e.fetcher != nil {
		client = e.fetcher.SafeClient()
	}

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
	const workers = 16
	// Dynamic threshold: 200 was right for huge crawls but left small ones
	// silent for minutes. Aim for ~10 progress events regardless of size.
	progressEvery := len(urls) / 10
	if progressEvery < 20 {
		progressEvery = 20
	}

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

				if e.ssrfGuard != nil {
					parsed, parseErr := url.Parse(pageURL)
					if parseErr != nil || e.ssrfGuard.ValidateURL(pageURL) != nil || e.ssrfGuard.ValidateHost(parsed.Hostname()) != nil {
						results[idx] = res
						continue
					}
				}

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

				if n := int(processed.Add(1)); n%progressEvery == 0 || n == len(urls) {
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
		slog.Error("engine: markdown negotiation issues batch insert failed", "err", err, "job_id", jobID)
	}

	slog.Info("engine: markdown negotiation summary", "supported", supportedCount, "total", total, "job_id", jobID)
}

// runTextQualityChecks runs LanguageTool on all crawled pages and creates
// issues for spelling/grammar errors found.
func (e *Engine) runTextQualityChecks(ctx context.Context, jobID string) {
	client := textquality.NewLTClient(e.config.LanguageToolURL)
	if !client.IsAvailable(ctx) {
		slog.Info("engine: LanguageTool not available, skipping text quality checks", "url", e.config.LanguageToolURL)
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
		slog.Error("engine: text quality query failed", "err", err, "job_id", jobID)
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
	slog.Info("engine: text quality custom dictionary loaded", "words", len(customDict))

	slog.Info("engine: running text quality checks via LanguageTool", "pages", len(pages), "job_id", jobID)
	e.emitPhase(jobID, "text_quality", fmt.Sprintf("checking spelling/grammar on %d pages via LanguageTool", len(pages)))
	tqProgressEvery := len(pages) / 10
	if tqProgressEvery < 5 {
		tqProgressEvery = 5
	}
	totalFindings := 0
	checkOpts := textquality.CheckOptions{CustomDict: customDict}

	for i, pg := range pages {
		if ctx.Err() != nil {
			break
		}
		if (i+1)%tqProgressEvery == 0 || i+1 == len(pages) {
			e.emitPhase(jobID, "text_quality_progress", fmt.Sprintf("checked %d/%d pages (%d findings so far)", i+1, len(pages), totalFindings))
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
			slog.Warn("engine: text quality check failed", "url", pg.url, "err", err)
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

	slog.Info("engine: text quality checks complete", "findings", totalFindings, "pages", len(pages), "job_id", jobID)
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
		slog.Error("engine: browser enrich: query failed", "err", err, "job_id", jobID)
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

	slog.Info("engine: browser enrich: re-rendering with full scroll", "pages", len(pages), "job_id", jobID)
	e.emitPhase(jobID, "browser_enrich", fmt.Sprintf("re-rendering %d JS-suspect pages to capture lazy content", len(pages)))
	progressEvery := len(pages) / 10
	if progressEvery < 5 {
		progressEvery = 5
	}
	enriched := 0

	for i, pg := range pages {
		if ctx.Err() != nil {
			break
		}
		if (i+1)%progressEvery == 0 || i+1 == len(pages) {
			e.emitPhase(jobID, "browser_enrich_progress", fmt.Sprintf("rendered %d/%d pages (%d enriched)", i+1, len(pages), enriched))
		}
		if err := e.validateRenderTarget(pg.url); err != nil {
			slog.Warn("engine: browser enrich: ssrf rejected", "url", pg.url, "err", err)
			continue
		}
		// Use content-only render (no menu clicks) to preserve page's own content
		pwResult, pwErr := renderer.RenderPageContentOnly(ctx, pg.url)
		if pwErr != nil {
			continue
		}
		page, parseErr := parser.ParseHTML([]byte(pwResult.HTML), pwResult.FinalURL, http.Header{})
		if parseErr != nil {
			continue
		}
		if page.ExtractedWordCount <= pg.wordCount {
			continue // static version already had equal or more content
		}

		enriched++
		if err := e.updateBrowserEnrichedPage(jobID, pg.urlID, page); err != nil {
			slog.Warn("engine: browser enrich: update page failed", "url", pg.url, "err", err)
			continue
		}

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

	slog.Info("engine: browser enrich: updated pages with richer content", "enriched", enriched, "total", len(pages), "job_id", jobID)
}

func (e *Engine) updateBrowserEnrichedPage(jobID string, urlID int64, page *parser.ParseResult) error {
	_, err := e.db.Exec(`
		UPDATE pages SET
			word_count = ?,
			main_content_word_count = ?,
			content_hash = ?,
			text_preview = ?,
			h1_json = ?,
			h2_json = ?,
			h3_json = ?,
			h4_json = ?,
			h5_json = ?,
			h6_json = ?,
			images_json = ?
		WHERE job_id = ? AND url_id = ?`,
		page.ExtractedWordCount, page.MainContentWordCount,
		page.ContentHash,
		limitTextPreview(page.ExtractedText),
		marshalStringSlice(page.Headings.H1),
		marshalStringSlice(page.Headings.H2),
		marshalStringSlice(page.Headings.H3),
		marshalStringSlice(page.Headings.H4),
		marshalStringSlice(page.Headings.H5),
		marshalStringSlice(page.Headings.H6),
		marshalImages(page.Images),
		jobID, urlID,
	)
	if err != nil {
		return err
	}
	return e.removeInvalidatedBrowserIssues(jobID, urlID, page)
}

func (e *Engine) removeInvalidatedBrowserIssues(jobID string, urlID int64, page *parser.ParseResult) error {
	issueTypes := []string{"js_suspect_not_rendered"}
	if len(page.Headings.H1) > 0 {
		issueTypes = append(issueTypes, "missing_h1")
	}
	if len(page.Headings.H2) > 0 {
		issueTypes = append(issueTypes, "missing_h2")
	}

	threshold := issues.DefaultThresholds().ThinContentThreshold
	if e.config != nil && e.config.ThinContentThreshold > 0 {
		threshold = e.config.ThinContentThreshold
	}
	if page.ExtractedWordCount >= threshold {
		issueTypes = append(issueTypes, "thin_content")
	}

	placeholders := make([]string, len(issueTypes))
	args := make([]any, 0, 2+len(issueTypes))
	args = append(args, jobID, urlID)
	for i, issueType := range issueTypes {
		placeholders[i] = "?"
		args = append(args, issueType)
	}

	_, err := e.db.Exec(`
		DELETE FROM issues
		WHERE job_id = ?
			AND url_id = ?
			AND scope = 'page_local'
			AND issue_type IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	return err
}

const maxTextPreviewRunes = 4000

func limitTextPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxTextPreviewRunes {
		return text
	}
	return string(runes[:maxTextPreviewRunes])
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

func hasFaviconAsset(assets []parser.DiscoveredAsset) bool {
	for _, asset := range assets {
		if asset.Type == "icon" {
			return true
		}
	}
	return false
}

// recalculatePageLinkCounts refreshes stored graph counters after post-crawl
// phases have added their final edges. Inbound metrics use the normalized target
// URL id captured on each edge; declared href text may differ from canonical URL
// shape after normalization.
func (e *Engine) recalculatePageLinkCounts(jobID string) error {
	if _, err := e.db.Exec(`
		UPDATE pages SET inbound_edge_count = (
			SELECT COUNT(*) FROM edges e
			WHERE e.job_id = pages.job_id
			  AND e.normalized_target_url_id = pages.url_id
			  AND e.is_internal = 1 AND e.relation_type = 'link'
		) WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("update inbound edge count: %w", err)
	}

	if _, err := e.db.Exec(`
		UPDATE pages SET inbound_linking_pages = (
			SELECT COUNT(DISTINCT e.source_url_id) FROM edges e
			WHERE e.job_id = pages.job_id
			  AND e.normalized_target_url_id = pages.url_id
			  AND e.is_internal = 1 AND e.relation_type = 'link'
		) WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("update inbound linking pages: %w", err)
	}

	if _, err := e.db.Exec(`
		UPDATE pages SET outbound_edge_count = (
			SELECT COUNT(*) FROM edges e
			WHERE e.job_id = pages.job_id
			  AND e.source_url_id = pages.url_id
			  AND e.is_internal = 1 AND e.relation_type = 'link'
		) WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("update outbound edge count: %w", err)
	}

	return nil
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
		JOIN urls tu ON tu.id = e.normalized_target_url_id AND tu.job_id = e.job_id
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
