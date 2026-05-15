package lighthouse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// PSIResult holds the response from PageSpeed Insights API.
type PSIResult struct {
	URL                string           `json:"url"`
	Strategy           string           `json:"strategy"` // "mobile" or "desktop"
	PerformanceScore   float64          `json:"performanceScore"`
	AccessibilityScore float64          `json:"accessibilityScore"`
	BestPracticesScore float64          `json:"bestPracticesScore"`
	SEOScore           float64          `json:"seoScore"`
	CoreWebVitals      CoreWebVitals    `json:"coreWebVitals"`
	Audits             map[string]Audit `json:"audits"`
}

type CoreWebVitals struct {
	LCP  float64 `json:"lcp"`  // Largest Contentful Paint (ms)
	FID  float64 `json:"fid"`  // First Input Delay (ms)
	CLS  float64 `json:"cls"`  // Cumulative Layout Shift
	FCP  float64 `json:"fcp"`  // First Contentful Paint (ms)
	TTFB float64 `json:"ttfb"` // Time to First Byte (ms)
	TBT  float64 `json:"tbt"`  // Total Blocking Time (ms)
	SI   float64 `json:"si"`   // Speed Index (ms)
}

type Audit struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Score        float64 `json:"score"` // 0-1
	DisplayValue string  `json:"displayValue"`
}

// keyAudits lists the audit IDs we extract from the PSI response.
var keyAudits = []string{
	"largest-contentful-paint", "first-contentful-paint",
	"cumulative-layout-shift", "total-blocking-time",
	"speed-index", "server-response-time",
	"render-blocking-resources", "unused-javascript",
	"unused-css-rules", "modern-image-formats",
	"uses-optimized-images", "uses-text-compression",
}

// ParsePSIResponse parses a raw PSI JSON response into a PSIResult.
// Exported for testing without calling the actual API.
func ParsePSIResponse(raw map[string]interface{}, pageURL, strategy string) *PSIResult {
	result := &PSIResult{URL: pageURL, Strategy: strategy}

	lr, ok := raw["lighthouseResult"].(map[string]interface{})
	if !ok {
		return result
	}

	if cats, ok := lr["categories"].(map[string]interface{}); ok {
		if p, ok := cats["performance"].(map[string]interface{}); ok {
			result.PerformanceScore, _ = p["score"].(float64)
		}
		if a, ok := cats["accessibility"].(map[string]interface{}); ok {
			result.AccessibilityScore, _ = a["score"].(float64)
		}
		if bp, ok := cats["best-practices"].(map[string]interface{}); ok {
			result.BestPracticesScore, _ = bp["score"].(float64)
		}
		if s, ok := cats["seo"].(map[string]interface{}); ok {
			result.SEOScore, _ = s["score"].(float64)
		}
	}

	audits, ok := lr["audits"].(map[string]interface{})
	if !ok {
		return result
	}

	result.Audits = make(map[string]Audit)
	// Persist ALL audits returned by PSI, not just a hardcoded subset.
	// This ensures accessibility, SEO, and best-practices audits are available
	// alongside performance audits.
	for key, raw := range audits {
		a, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		audit := Audit{ID: key}
		audit.Title, _ = a["title"].(string)
		audit.Description, _ = a["description"].(string)
		audit.Score, _ = a["score"].(float64)
		audit.DisplayValue, _ = a["displayValue"].(string)
		// Only include audits that have meaningful content (title or score)
		if audit.Title != "" || audit.Score > 0 {
			result.Audits[key] = audit
		}
	}

	// Extract Core Web Vitals from audit numeric values
	cwvMap := map[string]*float64{
		"largest-contentful-paint": &result.CoreWebVitals.LCP,
		"first-contentful-paint":  &result.CoreWebVitals.FCP,
		"cumulative-layout-shift": &result.CoreWebVitals.CLS,
		"total-blocking-time":     &result.CoreWebVitals.TBT,
		"speed-index":             &result.CoreWebVitals.SI,
		"server-response-time":    &result.CoreWebVitals.TTFB,
	}
	for auditKey, field := range cwvMap {
		if a, ok := audits[auditKey].(map[string]interface{}); ok {
			if nv, ok := a["numericValue"].(float64); ok {
				*field = nv
			}
		}
	}

	return result
}

// FetchPSI calls the PageSpeed Insights API for a URL.
func FetchPSI(ctx context.Context, pageURL, apiKey, strategy string) (*PSIResult, error) {
	endpoint := fmt.Sprintf(
		"https://www.googleapis.com/pagespeedonline/v5/runPagespeed?url=%s&key=%s&strategy=%s&category=PERFORMANCE&category=ACCESSIBILITY&category=BEST_PRACTICES&category=SEO",
		url.QueryEscape(pageURL), apiKey, strategy,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("PSI API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding PSI response: %w", err)
	}

	return ParsePSIResponse(raw, pageURL, strategy), nil
}
