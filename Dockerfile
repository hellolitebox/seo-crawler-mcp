FROM golang:1.25-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o seo-crawler-mcp .

FROM python:3.12-slim-bookworm
# System deps:
#  - chromium: shared by chromedp (Go renderer pool) AND Playwright (via
#    CHROMIUM_PATH below) so we don't ship two browsers in the image.
#  - sqlite3: CLI used by scripts/backup.sh.
#  - cron: daily backup runner.
#  - fonts-liberation: makes Chromium render text instead of tofu boxes.
#  - ca-certificates: TLS for outbound fetches.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    sqlite3 \
    cron \
    fonts-liberation \
    && rm -rf /var/lib/apt/lists/*

# Playwright Python — used for accessibility audits (axe-core via Playwright)
# and for the better menu-discovery / lazy-content render paths. We DO NOT
# run `playwright install`; the launch scripts honour CHROMIUM_PATH and reuse
# the apt-installed chromium so we keep one copy of the browser in the image.
RUN pip install --no-cache-dir playwright==1.49.1
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
ENV CHROMIUM_PATH=/usr/bin/chromium

WORKDIR /app
COPY --from=builder /app/seo-crawler-mcp .
COPY scripts/backup.sh /app/scripts/backup.sh
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/scripts/backup.sh /app/entrypoint.sh
RUN mkdir -p /data /data/backups
VOLUME ["/data"]
EXPOSE 8080
CMD ["/app/entrypoint.sh"]
