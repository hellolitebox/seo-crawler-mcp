// Package urlgroup auto-detects and assigns URL pattern groups for crawled pages.
package urlgroup

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

const minAutoGroupSize = 10

// DetectGroups auto-detects URL pattern groups from crawled pages.
func DetectGroups(db *storage.DB, jobID string, userGroups []config.URLGroupConfig) error {
	// Clear existing groups for this job
	if _, err := db.Exec(`DELETE FROM url_pattern_groups WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("clearing url pattern groups: %w", err)
	}

	// Reset all page url_group values
	if _, err := db.Exec(`UPDATE pages SET url_group = NULL WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("clearing page url_group: %w", err)
	}

	// Get all crawled page URLs
	rows, err := db.Query(`
		SELECT p.url_id, u.normalized_url
		FROM pages p
		JOIN urls u ON u.id = p.url_id AND u.job_id = p.job_id
		WHERE p.job_id = ?
	`, jobID)
	if err != nil {
		return fmt.Errorf("querying pages for grouping: %w", err)
	}
	defer rows.Close()

	type pageEntry struct {
		urlID  int64
		rawURL string
	}
	pages := []pageEntry{}
	for rows.Next() {
		var pe pageEntry
		if err := rows.Scan(&pe.urlID, &pe.rawURL); err != nil {
			return fmt.Errorf("scanning page for grouping: %w", err)
		}
		pages = append(pages, pe)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Build pattern counts from first two path segments
	patternURLs := map[string][]pageEntry{}
	for _, pe := range pages {
		pattern := extractPattern(pe.rawURL)
		if pattern == "" || pattern == "/" {
			continue
		}
		patternURLs[pattern] = append(patternURLs[pattern], pe)
	}

	// Process user-defined groups first (they take priority)
	userPatterns := map[string]string{} // pattern → name
	for _, ug := range userGroups {
		userPatterns[ug.Pattern] = ug.Name

		// Insert user group
		_, err := db.Exec(`INSERT INTO url_pattern_groups (job_id, pattern, name, source) VALUES (?, ?, ?, 'user')`,
			jobID, ug.Pattern, ug.Name)
		if err != nil {
			return fmt.Errorf("inserting user group %q: %w", ug.Name, err)
		}

		// Assign pages matching this pattern
		for _, pe := range pages {
			if matchesPattern(pe.rawURL, ug.Pattern) {
				if _, err := db.Exec(`UPDATE pages SET url_group = ? WHERE job_id = ? AND url_id = ?`,
					ug.Name, jobID, pe.urlID); err != nil {
					return fmt.Errorf("assigning url_group %q: %w", ug.Name, err)
				}
			}
		}
	}

	// Auto-detect groups for patterns with 10+ URLs (skip user-overridden patterns)
	for pattern, entries := range patternURLs {
		if len(entries) < minAutoGroupSize {
			continue
		}

		// Check if a user group already covers this pattern
		overridden := false
		for userPattern := range userPatterns {
			if userPattern == pattern || strings.HasPrefix(pattern, userPattern) {
				overridden = true
				break
			}
		}
		if overridden {
			continue
		}

		name := patternToName(pattern)

		_, err := db.Exec(`INSERT INTO url_pattern_groups (job_id, pattern, name, source) VALUES (?, ?, ?, 'auto')`,
			jobID, pattern, name)
		if err != nil {
			return fmt.Errorf("inserting auto group %q: %w", name, err)
		}

		for _, pe := range entries {
			// Don't override user-assigned groups
			var existing *string
			db.QueryRow(`SELECT url_group FROM pages WHERE job_id = ? AND url_id = ?`, jobID, pe.urlID).Scan(&existing)
			if existing != nil {
				continue
			}
			if _, err := db.Exec(`UPDATE pages SET url_group = ? WHERE job_id = ? AND url_id = ?`,
				name, jobID, pe.urlID); err != nil {
				return fmt.Errorf("assigning auto url_group %q: %w", name, err)
			}
		}
	}

	return nil
}

// extractPattern returns the first path segment as a pattern.
// e.g., "https://example.com/blog/post-1" → "/blog"
// For single-segment paths like "/about", returns "/about".
// For root "/", returns "/".
func extractPattern(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		return "/"
	}

	segments := strings.Split(path, "/")
	// segments[0] is empty string before leading /
	if len(segments) < 2 || segments[1] == "" {
		return "/"
	}

	return "/" + segments[1]
}

// patternToName converts a URL pattern to a human-readable group name.
// e.g., "/blog" → "blog", "/docs/api" → "docs-api"
func patternToName(pattern string) string {
	name := strings.TrimPrefix(pattern, "/")
	name = strings.ReplaceAll(name, "/", "-")
	if name == "" {
		return "root"
	}
	return name
}

// matchesPattern checks if a URL matches a given pattern prefix.
func matchesPattern(rawURL, pattern string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := u.Path
	if !strings.HasPrefix(path, pattern) {
		return false
	}
	// Ensure it's a prefix match at segment boundary
	rest := path[len(pattern):]
	return rest == "" || rest[0] == '/'
}
