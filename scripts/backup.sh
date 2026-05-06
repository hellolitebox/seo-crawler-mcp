#!/bin/sh
# SQLite backup script for the crawler.
# Run inside the Fly machine on a cron schedule, or invoked manually:
#   fly ssh console -a seo-crawler-mcp -C 'sh /app/scripts/backup.sh'
#
# Uses `sqlite3 .backup` which is safe on a live database (online backup API).
# Outputs are timestamped and the script keeps the last N backups.
#
# Where backups live:
#   /data/backups/crawls-YYYYMMDD-HHMMSS.db.gz
#
# Restore example:
#   gunzip -c /data/backups/crawls-...db.gz > /data/crawls.db

set -eu

DB="${SEO_CRAWLER_DB:-/data/crawls.db}"
BACKUP_DIR="${SEO_CRAWLER_BACKUP_DIR:-/data/backups}"
KEEP="${SEO_CRAWLER_BACKUP_KEEP:-7}"

mkdir -p "$BACKUP_DIR"

ts=$(date -u +'%Y%m%d-%H%M%S')
out="$BACKUP_DIR/crawls-$ts.db"

# Use the SQLite online backup API (safe on a running database).
sqlite3 "$DB" ".backup '$out'"
gzip -9 "$out"

echo "backup complete: $out.gz ($(du -h "$out.gz" | cut -f1))"

# Trim to the last N backups.
ls -1t "$BACKUP_DIR"/crawls-*.db.gz 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -v
