// Package llmstxt parses llms.txt files to extract sections and referenced URLs.
package llmstxt

import (
	"regexp"
	"strings"
)

// LlmsTxt represents a parsed llms.txt file.
type LlmsTxt struct {
	Sections []Section `json:"sections"`
	URLs     []string  `json:"urls"` // all discovered URLs
}

// Section represents a titled section within an llms.txt file.
type Section struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>")\]]+`)

// Parse parses llms.txt content into sections and extracts URLs.
func Parse(content string) *LlmsTxt {
	result := &LlmsTxt{
		Sections: []Section{},
		URLs:     []string{},
	}

	if strings.TrimSpace(content) == "" {
		return result
	}

	lines := strings.Split(content, "\n")
	currentTitle := ""
	contentLines := []string{}
	hasSections := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			// Flush previous section.
			if hasSections || len(contentLines) > 0 {
				result.Sections = append(result.Sections, Section{
					Title:   currentTitle,
					Content: strings.TrimSpace(strings.Join(contentLines, "\n")),
				})
			}
			// Extract title: strip leading #s and whitespace.
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			currentTitle = title
			contentLines = []string{}
			hasSections = true
			continue
		}
		contentLines = append(contentLines, line)
	}

	// Flush last section.
	if hasSections || len(contentLines) > 0 {
		result.Sections = append(result.Sections, Section{
			Title:   currentTitle,
			Content: strings.TrimSpace(strings.Join(contentLines, "\n")),
		})
	}

	// Extract URLs from entire content.
	matches := urlPattern.FindAllString(content, -1)
	seen := map[string]bool{}
	for _, u := range matches {
		if !seen[u] {
			seen[u] = true
			result.URLs = append(result.URLs, u)
		}
	}

	return result
}
