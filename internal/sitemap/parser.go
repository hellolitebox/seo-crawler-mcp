// Package sitemap provides parsing and fetching of XML sitemaps,
// including sitemap index files with recursive following.
package sitemap

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// maxBodySize caps sitemap reads at 20MB to prevent OOM.
const maxBodySize = 20 * 1024 * 1024

// maxSitemapFetches caps recursive sitemap-index traversal. maxEntries limits
// returned URLs, but index-only trees can otherwise force unbounded network fetches.
const maxSitemapFetches = 1000

// Entry represents a single URL entry from a sitemap.
type Entry struct {
	Loc        string  `xml:"loc" json:"loc"`
	Lastmod    string  `xml:"lastmod,omitempty" json:"lastmod,omitempty"`
	Changefreq string  `xml:"changefreq,omitempty" json:"changefreq,omitempty"`
	Priority   float64 `xml:"priority,omitempty" json:"priority,omitempty"`
	SourceURL  string  `json:"sourceUrl"`
}

type urlSet struct {
	URLs []Entry `xml:"url"`
}

type sitemapIndex struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

// ParseXML parses a sitemap <urlset> XML and returns the URL entries.
func ParseXML(data []byte) ([]Entry, error) {
	var us urlSet
	if err := xml.Unmarshal(data, &us); err != nil {
		return nil, fmt.Errorf("parsing urlset XML: %w", err)
	}
	entries := make([]Entry, len(us.URLs))
	copy(entries, us.URLs)
	return entries, nil
}

// ParseIndex parses a <sitemapindex> XML and returns the list of sitemap URLs.
func ParseIndex(data []byte) ([]string, error) {
	var si sitemapIndex
	if err := xml.Unmarshal(data, &si); err != nil {
		return nil, fmt.Errorf("parsing sitemapindex XML: %w", err)
	}
	urls := make([]string, 0, len(si.Sitemaps))
	for _, s := range si.Sitemaps {
		urls = append(urls, s.Loc)
	}
	return urls, nil
}

// FetchAndParse fetches a sitemap URL, auto-detects index vs urlset,
// recursively follows index children, and respects maxEntries limit.
// Returns entries, count of sitemaps processed, and any error.
func FetchAndParse(sitemapURL string, maxEntries int, client *http.Client) ([]Entry, int, error) {
	return FetchAndParseContext(context.Background(), sitemapURL, maxEntries, client)
}

// FetchAndParseContext fetches a sitemap URL with cancellation support, auto-detects index vs urlset,
// recursively follows index children, and respects maxEntries limit.
func FetchAndParseContext(ctx context.Context, sitemapURL string, maxEntries int, client *http.Client) ([]Entry, int, error) {
	entries := make([]Entry, 0)
	visited := make(map[string]bool)
	queue := []string{sitemapURL}
	sitemapCount := 0

	for len(queue) > 0 && len(entries) < maxEntries {
		if err := ctx.Err(); err != nil {
			return entries, sitemapCount, err
		}
		if sitemapCount >= maxSitemapFetches {
			return entries, sitemapCount, fmt.Errorf("sitemap traversal exceeded %d fetched sitemaps", maxSitemapFetches)
		}
		current := queue[0]
		queue = queue[1:]

		if visited[current] {
			continue
		}
		visited[current] = true

		data, err := fetchSitemapContent(ctx, current, client)
		if err != nil {
			slog.Warn("sitemap: skipping unreadable", "url", current, "err", err)
			continue
		}

		sitemapCount++

		// Try as index first.
		indexURLs, err := ParseIndex(data)
		if err == nil && len(indexURLs) > 0 {
			if len(visited)+len(queue)+len(indexURLs) > maxSitemapFetches {
				return entries, sitemapCount, fmt.Errorf("sitemap traversal exceeded %d queued sitemaps", maxSitemapFetches)
			}
			queue = append(queue, indexURLs...)
			continue
		}

		// Try as urlset.
		parsed, err := ParseXML(data)
		if err != nil {
			slog.Warn("sitemap: skipping malformed", "url", current, "err", err)
			continue
		}

		remaining := maxEntries - len(entries)
		if len(parsed) > remaining {
			parsed = parsed[:remaining]
		}

		for i := range parsed {
			parsed[i].SourceURL = current
		}
		entries = append(entries, parsed...)
	}

	return entries, sitemapCount, nil
}

// fetchSitemapContent fetches a URL and returns the raw body bytes,
// handling gzip decompression when needed.

func fetchSitemapContent(ctx context.Context, rawURL string, client *http.Client) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %q: %w", rawURL, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %q: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %q: status %d", rawURL, resp.StatusCode)
	}

	var reader io.Reader = io.LimitReader(resp.Body, maxBodySize)

	// Go's HTTP transport auto-decompresses Content-Encoding: gzip and sets
	// resp.Uncompressed=true. We only need manual decompression for .gz files
	// served as raw bytes (no Content-Encoding header).
	needsGunzip := strings.HasSuffix(rawURL, ".gz") && !resp.Uncompressed
	if needsGunzip {
		gr, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("creating gzip reader for %q: %w", rawURL, err)
		}
		defer gr.Close()
		reader = io.LimitReader(gr, maxBodySize)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("reading body from %q: %w", rawURL, err)
	}
	return data, nil
}
