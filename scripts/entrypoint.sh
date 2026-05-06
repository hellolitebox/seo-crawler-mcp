#!/bin/sh
# Container entrypoint: starts dcron in the background to run daily SQLite
# backups, then execs the crawler HTTP server in the foreground.
set -eu

# Schedule: 03:17 UTC every day. Spread off-hour to avoid clashing with peaks.
CRON_LINE='17 3 * * * /app/scripts/backup.sh >> /var/log/cron-backup.log 2>&1'

mkdir -p /etc/crontabs /var/log
echo "$CRON_LINE" > /etc/crontabs/root

# Start crond in the background. -f keeps it in foreground; we want background here.
crond -b -L /var/log/crond.log

exec ./seo-crawler-mcp --http :8080 --db /data/crawls.db
