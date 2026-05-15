package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/engine"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/httpserver"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/mcp"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/renderer"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	checkConfig := flag.Bool("check-config", false, "Validate and print effective config")
	configPath := flag.String("config", "", "Path to config file (TOML)")
	dbPath := flag.String("db", "", "Path to SQLite database")
	httpAddr := flag.String("http", "", "Start HTTP server on this address (e.g. :8080). "+
		"NOTE: per-IP rate limiting and SSE caps trust X-Forwarded-For and Fly-Client-IP "+
		"headers from upstream proxies. Run behind a reverse proxy that strips/rewrites "+
		"these headers; direct internet exposure lets clients spoof their IP and bypass limits.")
	flag.Parse()

	if *showVersion {
		fmt.Printf("seo-crawler-mcp %s\n", version)
		os.Exit(0)
	}

	// Handle "purge" subcommand.
	if flag.NArg() > 0 && flag.Arg(0) == "purge" {
		runPurge(flag.Args()[1:])
		return
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// CLI flag overrides.
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	if *checkConfig {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(cfg)
		os.Exit(0)
	}

	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Reap zombie jobs from a previous process that died mid-crawl.
	if n, err := db.MarkOrphanedJobsFailed("server restarted while crawling"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to reap orphaned jobs: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "reaped %d orphaned job(s)\n", n)
	}

	// Pending purges from a process that died mid-DELETE are kept tombstoned by
	// default instead of auto-resumed on startup. Large purges share the SQLite
	// connection and can stall read endpoints during cold start; set
	// SEO_CRAWLER_RESUME_PURGES_ON_STARTUP=1 for explicit maintenance windows.
	pendingPurges, _ := db.ResumePendingPurges()

	guard := ssrf.NewGuard(cfg.AllowPrivateNetworks)

	f := fetcher.New(fetcher.Options{
		UserAgent:           cfg.UserAgent,
		Timeout:             cfg.RequestTimeout,
		MaxResponseBody:     cfg.MaxResponseBody,
		MaxDecompressedBody: cfg.MaxDecompressedBody,
		MaxRedirectHops:     cfg.MaxRedirectHops,
		Retries:             cfg.Retries,
		AllowInsecureTLS:    cfg.AllowInsecureTLS,
		SSRFGuard:           guard,
	})

	// Renderer pool (only for non-static modes).
	var renderPool *renderer.Pool
	if cfg.RenderMode != config.RenderModeStatic {
		renderPool = renderer.NewPool(renderer.Options{
			MaxSlots:      cfg.MaxBrowserInstances,
			RenderWaitMs:  cfg.RenderWaitMs,
			RenderTimeout: cfg.BrowserRenderTimeout,
		})
		defer renderPool.Close()
	}
	// renderPool is passed to the engine for hybrid rendering

	rl := fetcher.NewRateLimiter(cfg.PerHostConcurrency)

	// Note: ScopeChecker is per-crawl (needs seed host), set to nil here.
	// The engine/tools create it per job when a crawl starts.
	eng := engine.New(engine.EngineConfig{
		DB:          db,
		Fetcher:     f,
		RateLimiter: rl,
		SSRFGuard:   guard,
		Config:      cfg,
		Renderer:    renderPool,
	})

	if *httpAddr != "" {
		httpSrv := httpserver.New(db, eng, cfg)
		if len(pendingPurges) > 0 {
			if os.Getenv("SEO_CRAWLER_RESUME_PURGES_ON_STARTUP") == "1" {
				for _, id := range pendingPurges {
					httpSrv.EnqueuePurge(id)
				}
				fmt.Fprintf(os.Stderr, "resumed %d pending purge(s)\n", len(pendingPurges))
			} else {
				fmt.Fprintf(os.Stderr, "left %d pending purge(s) tombstoned; set SEO_CRAWLER_RESUME_PURGES_ON_STARTUP=1 to resume\n", len(pendingPurges))
			}
		}
		srv := &http.Server{
			Addr:              *httpAddr,
			Handler:           httpSrv.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      0, // Keep SSE streams viable.
			IdleTimeout:       120 * time.Second,
		}

		go func() {
			slog.Info("http server listening", "addr", *httpAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("http server failed", "err", err)
				os.Exit(1)
			}
		}()

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("http shutdown failed", "err", err)
		}
		return
	}

	srv := mcp.NewServer(mcp.ServerConfig{
		DB:      db,
		Engine:  eng,
		Fetcher: f,
		Config:  cfg,
	})

	fmt.Fprintln(os.Stderr, "seo-crawler-mcp: starting MCP server on stdio")
	if err := srv.ServeStdio(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
