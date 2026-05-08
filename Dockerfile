FROM golang:1.25-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Strip pip-wheels/ from the Go build context: Go interprets a
# top-level `vendor/`-shaped dir loosely and gets confused by random
# binaries living next to go.mod.
RUN rm -rf pip-wheels && CGO_ENABLED=0 GOOS=linux go build -o seo-crawler-mcp .

# Python 3.10 slim, matching the cp310 wheels in vendor/. The Fly
# remote builder can't reach PyPI reliably (connection reset by peer,
# 5/5 retries), so we install Playwright + deps from pre-downloaded
# wheels copied into the image. Re-fetch with scripts/fetch-vendor-wheels.sh
# whenever the Playwright pin in this file changes.
FROM python:3.10-slim-bookworm

# System deps:
#  - chromium: shared by chromedp (Go) and Playwright (Python) via
#    CHROMIUM_PATH below — one browser binary in the image.
#  - sqlite3: CLI used by scripts/backup.sh.
#  - cron: daily backup runner.
#  - fonts-liberation: real text glyphs in screenshots / rendered HTML.
#  - ca-certificates: refreshed TLS roots.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    sqlite3 \
    cron \
    fonts-liberation \
    && rm -rf /var/lib/apt/lists/*

# Install Playwright + deps from vendored wheels (no PyPI reachout).
COPY pip-wheels/*.whl /tmp/wheels/
RUN pip install --no-cache-dir --no-index /tmp/wheels/*.whl && rm -rf /tmp/wheels

# Both chromedp and our Python launch scripts honour CHROMIUM_PATH so
# they share one browser binary instead of carrying two copies.
ENV CHROMIUM_PATH=/usr/bin/chromium
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1

WORKDIR /app
COPY --from=builder /app/seo-crawler-mcp .
COPY scripts/backup.sh /app/scripts/backup.sh
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/scripts/backup.sh /app/entrypoint.sh
RUN mkdir -p /data /data/backups
VOLUME ["/data"]
EXPOSE 8080
CMD ["/app/entrypoint.sh"]
