package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
)

// AxeResult holds the results from an axe-core accessibility audit.
type AxeResult struct {
	URL        string     `json:"url"`
	Violations []AxeIssue `json:"violations"`
	Passes     []AxeIssue `json:"passes"`
	Incomplete int        `json:"incomplete"`
	Error      string     `json:"error,omitempty"`
}

// AxeIssue represents a single accessibility violation found by axe-core.
type AxeIssue struct {
	ID          string   `json:"id"`
	Impact      string   `json:"impact"` // "critical", "serious", "moderate", "minor"
	Description string   `json:"description"`
	Help        string   `json:"help"`
	HelpURL     string   `json:"helpUrl"`
	Tags        []string `json:"tags"`  // WCAG tags
	Nodes       int      `json:"nodes"` // Number of affected elements
}

// IsPublicURL returns true if the URL resolves to a public host.
func IsPublicURL(rawURL string) bool {
	guard := ssrf.NewGuard(false)
	if err := guard.ValidateURL(rawURL); err != nil {
		return false
	}
	parsedHost, err := urlHostname(rawURL)
	if err != nil {
		return false
	}
	return guard.ValidateHost(parsedHost) == nil
}

func urlHostname(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return parsed.Hostname(), nil
}

// RunAxeAudit runs an axe-core accessibility audit on a single URL using Playwright.
// Requires python3 and playwright to be installed. Skips non-public URLs.
func RunAxeAudit(ctx context.Context, pageURL string) (*AxeResult, error) {
	results, err := RunAxeAuditBatch(ctx, []string{pageURL})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results returned for %s", pageURL)
	}
	if results[0].Error != "" {
		return nil, fmt.Errorf("axe audit failed for %s: %s", pageURL, results[0].Error)
	}
	return results[0], nil
}

// RunAxeAuditBatch runs axe-core accessibility audits on multiple URLs using a single
// Playwright browser instance. This avoids the overhead of launching a new browser
// for each page. Skips non-public URLs.
func RunAxeAuditBatch(ctx context.Context, urls []string) ([]*AxeResult, error) {
	// Filter to public URLs only
	var publicURLs []string
	for _, u := range urls {
		if IsPublicURL(u) {
			publicURLs = append(publicURLs, u)
		}
	}
	if len(publicURLs) == 0 {
		return nil, fmt.Errorf("no public URLs to audit")
	}

	// Generous timeout: 45s per URL + 15s overhead for browser launch
	timeout := time.Duration(len(publicURLs)*45+15) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Pass URLs as JSON via stdin to avoid shell escaping issues
	urlsJSON, err := json.Marshal(publicURLs)
	if err != nil {
		return nil, fmt.Errorf("marshalling URLs: %w", err)
	}

	cmd := exec.CommandContext(ctx, "python3", "-c", axeBatchScript())
	cmd.Stdin = strings.NewReader(string(urlsJSON))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("axe batch audit failed: %w", err)
	}

	var results []*AxeResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("parsing axe batch output: %w", err)
	}
	for _, result := range results {
		if result != nil && result.Error != "" {
			result.Violations = nil
			result.Passes = nil
		}
	}
	return results, nil
}

func axeBatchScript() string {
	return `
import os, sys, json
from playwright.sync_api import sync_playwright

urls = json.loads(sys.stdin.read())

results = []

_chromium_path = os.environ.get("CHROMIUM_PATH") or None

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=_chromium_path)

    for url in urls:
        try:
            page = browser.new_page(viewport={"width": 1440, "height": 900})
            page.goto(url, wait_until="networkidle", timeout=30000)
            page.wait_for_timeout(1000)

            # Inject axe-core from CDN
            page.add_script_tag(url="https://cdnjs.cloudflare.com/ajax/libs/axe-core/4.10.2/axe.min.js")
            page.wait_for_timeout(500)

            # Run axe
            axe_results = page.evaluate("""
                () => new Promise((resolve, reject) => {
                    axe.run(document, {
                        runOnly: {
                            type: 'tag',
                            values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']
                        }
                    }).then(results => {
                        const mapIssue = v => ({
                            id: v.id,
                            impact: v.impact || '',
                            description: v.description,
                            help: v.help,
                            helpUrl: v.helpUrl,
                            tags: v.tags,
                            nodes: v.nodes.length
                        });
                        resolve({
                            violations: results.violations.map(mapIssue),
                            passes: results.passes.map(mapIssue),
                            incomplete: results.incomplete.length
                        });
                    }).catch(reject);
                })
            """)

            results.append({
                "url": url,
                "violations": axe_results["violations"],
                "passes": axe_results["passes"],
                "incomplete": axe_results["incomplete"]
            })

            page.close()
        except Exception as e:
            # Record error but continue with other URLs
            results.append({
                "url": url,
                "violations": [],
                "passes": [],
                "incomplete": 0,
                "error": str(e)
            })
            try:
                page.close()
            except:
                pass

    browser.close()

print(json.dumps(results))
`
}
