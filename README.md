# seo-crawler-mcp

A deterministic SEO spider exposed as both an MCP server and an HTTP API. Written in Go, single binary, SQLite-backed.

## Live deployment

- **HTTP API**: https://seo-crawler-mcp.fly.dev
- **Hosting**: Fly.io (shared-cpu-1x, 512MB, auto-sleep when idle)
- **Persistent storage**: Fly volume (`seo_data`) mounted at `/data`

## Features

- **44 issue types** — 27 page-local + 17 global
- **Hybrid JS rendering** — static HTML by default, Chromedp for JS-heavy pages
- **Adaptive 429 retry** — respects Retry-After headers
- **SSRF protection** — blocks internal/private IP ranges
- **Crawl trap detection** — avoids infinite pagination loops
- **10 MCP tools** — `crawl_site`, `crawl_status`, `cancel_crawl`, `get_crawl_summary`, `get_crawl_results`, `get_link_graph`, `analyze_url`, `check_redirects`, `check_robots_txt`, `parse_sitemap`
- **5 MCP resources** (4 templates), **2 MCP prompts**

## HTTP API

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/crawl` | Start a crawl — body: `{ url, maxPages?, renderMode? }` → `{ jobId }` |
| `GET`  | `/api/jobs/:id` | Poll status → `{ status, pagesCrawled, issuesFound, urlsDiscovered }` |
| `GET`  | `/api/jobs/:id/report` | Full report JSON |
| `GET`  | `/health` | Health check → `{ ok: true }` |

### Render modes
- `auto` — static HTML, falls back to headless Chrome if JS signals detected
- `static` — HTML only (faster)
- `browser` — always use headless Chrome

## Running locally

```bash
# Build
go build -o seo-crawler-mcp .

# Run HTTP server
./seo-crawler-mcp --http :8080 --db /tmp/crawls.db

# Run as MCP stdio server
./seo-crawler-mcp --db /tmp/crawls.db

# Purge old jobs
./seo-crawler-mcp purge --older-than 30d --db /tmp/crawls.db
```

## Config

Priority: CLI flags > env vars (`SEO_CRAWLER_*`) > TOML config file (`--config`)

```bash
./seo-crawler-mcp --config config.toml --db /data/crawls.db --http :8080
```

## Deploying to Fly.io

```bash
fly deploy --remote-only
```

Requires `fly.toml` in the project root. Volume `seo_data` must exist in the target region:

```bash
fly volumes create seo_data --region iad --size 1
```

## Project structure

```
cmd/                  CLI entrypoint
internal/
  httpserver/         HTTP API layer
  engine/             Crawl engine
  storage/            SQLite persistence
  config/             Config parsing
  issues/             Issue detection (44 types)
Dockerfile
fly.toml
```

## Stats

- ~19K lines of Go across 48 source files
- 46 test files
- 428 pages/sec benchmark (static mode)
- CGO-free build via `modernc.org/sqlite`
