package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

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
	httpAddr := flag.String("http", "", "Start HTTP server on this address (e.g. :8080)")
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
		log.Printf("HTTP server listening on %s", *httpAddr)
		log.Fatal(http.ListenAndServe(*httpAddr, httpSrv.Handler()))
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
