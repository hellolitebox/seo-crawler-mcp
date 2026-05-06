FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o seo-crawler-mcp .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates chromium chromium-chromedriver
WORKDIR /app
COPY --from=builder /app/seo-crawler-mcp .
RUN mkdir -p /data
VOLUME ["/data"]
EXPOSE 8080
ENV CHROMIUM_PATH=/usr/bin/chromium-browser
CMD ["./seo-crawler-mcp", "--http", ":8080", "--db", "/data/crawls.db"]
