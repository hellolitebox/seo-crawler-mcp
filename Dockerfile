FROM golang:1.25-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o seo-crawler-mcp .

# Microsoft's official Playwright image — Ubuntu Jammy with Python 3.10,
# the playwright pip package, and its bundled browsers (Chromium, Firefox,
# WebKit) already installed. Avoids fetching from PyPI at image build
# time, which has been flaky from the Fly remote builder.
FROM mcr.microsoft.com/playwright/python:v1.49.1-jammy

# Extra runtime needs:
#  - sqlite3: CLI used by scripts/backup.sh.
#  - cron: daily backup runner.
#  - ca-certificates: refreshed TLS roots.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    sqlite3 \
    cron \
    && rm -rf /var/lib/apt/lists/*

# Expose Playwright's bundled Chromium at a stable path so both chromedp
# (Go renderer pool) and the Playwright Python launch scripts use the
# same binary via CHROMIUM_PATH. The directory name embeds Playwright's
# build number (e.g. chromium-1148) which changes with every release —
# resolve it once at build time and symlink to a fixed path.
RUN ln -s "$(find /ms-playwright -path '*chromium*' -name chrome -type f | head -1)" /usr/bin/chromium
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
