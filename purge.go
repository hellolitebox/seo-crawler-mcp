package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// runPurge handles the "purge" subcommand for cleaning up old crawl jobs.
func runPurge(args []string) {
	fs := flag.NewFlagSet("purge", flag.ExitOnError)
	olderThan := fs.String("older-than", "", "Purge jobs older than duration (e.g., 30d, 24h)")
	jobID := fs.String("job", "", "Purge specific job by ID")
	dbPath := fs.String("db", "seo-crawler.db", "Database path")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: seo-crawler-mcp purge [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *olderThan == "" && *jobID == "" {
		fmt.Fprintln(os.Stderr, "purge: specify --older-than or --job")
		fs.Usage()
		os.Exit(1)
	}

	db, err := storage.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "purge: database error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Enable foreign keys for CASCADE deletes.
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		fmt.Fprintf(os.Stderr, "purge: enabling foreign keys: %v\n", err)
		os.Exit(1)
	}

	if *jobID != "" {
		result, err := db.Exec("DELETE FROM crawl_jobs WHERE id = ?", *jobID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "purge: deleting job %q: %v\n", *jobID, err)
			os.Exit(1)
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			fmt.Fprintf(os.Stderr, "purge: no job found with id %q\n", *jobID)
			os.Exit(1)
		}
		fmt.Printf("purged job %s\n", *jobID)
		return
	}

	dur, err := parseDuration(*olderThan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "purge: invalid --older-than %q: %v\n", *olderThan, err)
		os.Exit(1)
	}

	threshold := time.Now().UTC().Add(-dur).Format(time.RFC3339)
	result, err := db.Exec(
		"DELETE FROM crawl_jobs WHERE finished_at IS NOT NULL AND finished_at < ?",
		threshold,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "purge: deleting old jobs: %v\n", err)
		os.Exit(1)
	}

	n, _ := result.RowsAffected()
	fmt.Printf("purged %d job(s) older than %s\n", n, *olderThan)
}

// durationRe matches human-friendly durations like "30d", "24h", "2h30m".
var durationRe = regexp.MustCompile(`^(\d+)d$`)

// parseDuration extends time.ParseDuration with support for "Nd" (days).
func parseDuration(s string) (time.Duration, error) {
	if m := durationRe.FindStringSubmatch(s); m != nil {
		days, _ := strconv.Atoi(m[1])
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
