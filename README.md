# seo-crawler-mcp

Deterministic SEO spider exposed as both an MCP server and an HTTP API.
Single Go binary, SQLite-backed, runs as a single Fly.io machine.

## Live deployment

- **HTTP API**: https://seo-crawler-mcp.fly.dev
- **Hosting**: Fly.io (`shared-cpu-2x` 4 GB, auto-sleep when idle)
- **Storage**: Fly volume `seo_data` mounted at `/data` (SQLite + nightly backups)
- **Frontend**: https://seo-crawler-report.vercel.app (companion repo `seo-crawler-report`)

## Features

- 44 issue types — 27 page-local + 17 global
- Hybrid JS rendering — static HTML by default, Chromedp for JS-heavy pages
- Adaptive 429 retry with `Retry-After` honored
- SSRF protection (blocks RFC1918 / link-local / loopback)
- Crawl-trap detection (avoids infinite pagination)
- 10 MCP tools, 5 resources, 2 prompts
- Per-IP rate limit on `/api/crawl` (10 crawls / hour)
- Server-Sent Events feed for live UIs
- Orphan job reaper on startup (no more stuck "running" rows after a deploy)
- Daily SQLite online backup with 7-day rotation

## HTTP API

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/health` | `{"ok": true}` — used by the Fly health check |
| `POST` | `/api/crawl` | Start a crawl. Body: `{ url, maxPages?, renderMode? }` → `{ jobId }`. Rate-limited per IP. |
| `GET`  | `/api/jobs?limit=N&offset=N` | Paginated list of crawl jobs (most recent first). |
| `GET`  | `/api/jobs/{id}` | Job status snapshot. |
| `DELETE` | `/api/jobs/{id}` | Cancel a running job, or purge a completed/failed one (cascades to all related rows). |
| `GET`  | `/api/jobs/{id}/report` | Full report JSON. |
| `GET`  | `/api/jobs/{id}/activity?limit=N` | Recent fetch + phase events (one-shot). |
| `GET`  | `/api/jobs/{id}/stream` | **Server-Sent Events** — pushes `status`, `activity` and `done` events while the crawl runs. |

### Render modes

- `auto` — static HTML, falls back to headless Chromium if JS signals are detected
- `static` — HTML only (faster)
- `browser` — always use headless Chromium

### CORS

Allowed origins are whitelisted (no `*`):

```
https://seo-crawler-report.vercel.app
http://localhost:4321
http://localhost:3000
```

Override with `ALLOWED_ORIGINS=https://a.com,https://b.com`.

## Running locally

```bash
go build -o seo-crawler-mcp .

# HTTP server
./seo-crawler-mcp --http :8080 --db /tmp/crawls.db

# MCP stdio
./seo-crawler-mcp --db /tmp/crawls.db

# Purge old jobs (manual maintenance)
./seo-crawler-mcp purge --older-than 30d --db /tmp/crawls.db
```

Config priority: CLI flags > env vars (`SEO_CRAWLER_*`) > TOML file (`--config`).

## Tests

```bash
go test ./...
```

Coverage includes:
- `internal/httpserver/rate_limiter_test.go` — per-key, limits, window reset
- `internal/httpserver/handlers_test.go` — list pagination, job status, delete (purge / cancel / 404), CORS allowed/denied/preflight
- `internal/httpserver/stream_test.go` — SSE headers, initial status snapshot, terminal `done` event, phase activity delivery
- `internal/storage/jobs_orphan_test.go` — startup reaper transitions in-flight jobs to `failed`
- `internal/engine/...` — extensive engine coverage (44 issue detectors, link graph, sitemap gap, etc.)

Total: ~60 backend tests, run in <60 s.

## Deploying to Fly.io

```bash
flyctl deploy --remote-only
```

Requires `fly.toml` in the project root and a volume `seo_data` in the target region:

```bash
flyctl volumes create seo_data --region iad --size 1
```

The Docker image installs `chromium`, `sqlite` (CLI for the backup script) and
`dcron` (runs the backup cron). `scripts/entrypoint.sh` starts crond in the
background and execs the crawler in the foreground.

### Backups

`scripts/backup.sh` runs daily at 03:17 UTC inside the Fly machine via dcron.
It uses the SQLite online backup API (safe on a live DB), gzips the result,
and keeps the last 7. Backups land at `/data/backups/crawls-YYYYMMDD-HHMMSS.db.gz`.

Manual run:
```bash
fly ssh console -a seo-crawler-mcp -C 'sh /app/scripts/backup.sh'
```

Restore:
```bash
gunzip -c /data/backups/crawls-20260506-031700.db.gz > /data/crawls.db
```

## Project structure

```
cmd/                           CLI entrypoint
internal/
  httpserver/
    server.go                  Routing, CORS, rate limit, slog logging
    handlers.go                /api/crawl, /api/jobs, /api/jobs/:id, ...
    stream.go                  SSE handler (streamSession struct, cached counts)
    *_test.go                  Handler + rate limiter + stream tests
  engine/                      Crawl engine (fetcher, parser, link graph, ...)
  storage/
    db.go                      SQLite open / migrations
    jobs.go                    CrawlJob CRUD + ListJobsPaginated + MarkOrphanedJobsFailed
    activity.go                GetFetchesSince / GetPhaseEventsSince (shared by /activity + /stream)
    ...                        urls, fetches, pages, issues, edges, events
  issues/                      Issue detection (44 types)
  ssrf/                        SSRF guard
  config/                      Config loader
scripts/
  backup.sh                    Daily SQLite backup with 7-day rotation
  entrypoint.sh                Container entrypoint: dcron + server
Dockerfile
fly.toml
```

## Stats

- ~19 K lines of Go across 50+ source files
- ~50 test files
- 428 pages/sec benchmark (static mode, single core)
- CGO-free build via `modernc.org/sqlite`
- Image: ~260 MB (Alpine + Chromium)
