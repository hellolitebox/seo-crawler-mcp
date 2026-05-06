FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o seo-crawler-mcp .

FROM alpine:3.20
# `sqlite` ships the sqlite3 CLI used by scripts/backup.sh; `dcron` runs the daily backup.
RUN apk add --no-cache ca-certificates chromium chromium-chromedriver sqlite dcron
WORKDIR /app
COPY --from=builder /app/seo-crawler-mcp .
COPY scripts/backup.sh /app/scripts/backup.sh
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/scripts/backup.sh /app/entrypoint.sh
RUN mkdir -p /data /data/backups
VOLUME ["/data"]
EXPOSE 8080
ENV CHROMIUM_PATH=/usr/bin/chromium-browser
CMD ["/app/entrypoint.sh"]
