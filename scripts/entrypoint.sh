#!/bin/sh
# Container entrypoint: starts cron in the background to run daily SQLite
# backups, then execs the crawler HTTP server in the foreground.
set -eu

# Schedule: 03:17 UTC every day. Spread off-hour to avoid clashing with peaks.
# /etc/cron.d entries include the user field (Debian convention).
CRON_FILE=/etc/cron.d/seo-crawler-backup
mkdir -p /etc/cron.d /var/log
cat > "$CRON_FILE" <<'EOF'
17 3 * * * root /app/scripts/backup.sh >> /var/log/cron-backup.log 2>&1
EOF
chmod 0644 "$CRON_FILE"

# Start cron in the background; the foreground process is the crawler.
cron

exec ./seo-crawler-mcp --http :8080 --db /data/crawls.db
