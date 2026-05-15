package lighthouse

import (
	"encoding/json"
	"testing"
)

func TestParsePSIResponse(t *testing.T) {
	// Simulate a minimal PSI API response structure
	rawJSON := `{
		"lighthouseResult": {
			"categories": {
				"performance": {"score": 0.85},
				"accessibility": {"score": 0.92},
				"best-practices": {"score": 0.78},
				"seo": {"score": 0.95}
			},
			"audits": {
				"largest-contentful-paint": {
					"id": "largest-contentful-paint",
					"title": "Largest Contentful Paint",
					"description": "LCP marks the time at which the largest text or image is painted.",
					"score": 0.75,
					"displayValue": "2.5 s",
					"numericValue": 2500
				},
				"first-contentful-paint": {
					"id": "first-contentful-paint",
					"title": "First Contentful Paint",
					"score": 0.9,
					"displayValue": "1.2 s",
					"numericValue": 1200
				},
				"cumulative-layout-shift": {
					"id": "cumulative-layout-shift",
					"title": "Cumulative Layout Shift",
					"score": 0.98,
					"displayValue": "0.05",
					"numericValue": 0.05
				},
				"total-blocking-time": {
					"id": "total-blocking-time",
					"title": "Total Blocking Time",
					"score": 0.8,
					"displayValue": "150 ms",
					"numericValue": 150
				},
				"speed-index": {
					"id": "speed-index",
					"title": "Speed Index",
					"score": 0.85,
					"displayValue": "3.0 s",
					"numericValue": 3000
				},
				"server-response-time": {
					"id": "server-response-time",
					"title": "Initial server response time was short",
					"score": 1.0,
					"displayValue": "Root document took 120 ms",
					"numericValue": 120
				}
			}
		}
	}`

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		t.Fatalf("failed to unmarshal test JSON: %v", err)
	}

	result := ParsePSIResponse(raw, "https://example.com", "mobile")

	// Verify basic fields
	if result.URL != "https://example.com" {
		t.Errorf("URL = %q, want %q", result.URL, "https://example.com")
	}
	if result.Strategy != "mobile" {
		t.Errorf("Strategy = %q, want %q", result.Strategy, "mobile")
	}

	// Verify category scores
	if result.PerformanceScore != 0.85 {
		t.Errorf("PerformanceScore = %v, want 0.85", result.PerformanceScore)
	}
	if result.AccessibilityScore != 0.92 {
		t.Errorf("AccessibilityScore = %v, want 0.92", result.AccessibilityScore)
	}
	if result.BestPracticesScore != 0.78 {
		t.Errorf("BestPracticesScore = %v, want 0.78", result.BestPracticesScore)
	}
	if result.SEOScore != 0.95 {
		t.Errorf("SEOScore = %v, want 0.95", result.SEOScore)
	}

	// Verify Core Web Vitals
	if result.CoreWebVitals.LCP != 2500 {
		t.Errorf("CWV.LCP = %v, want 2500", result.CoreWebVitals.LCP)
	}
	if result.CoreWebVitals.FCP != 1200 {
		t.Errorf("CWV.FCP = %v, want 1200", result.CoreWebVitals.FCP)
	}
	if result.CoreWebVitals.CLS != 0.05 {
		t.Errorf("CWV.CLS = %v, want 0.05", result.CoreWebVitals.CLS)
	}
	if result.CoreWebVitals.TBT != 150 {
		t.Errorf("CWV.TBT = %v, want 150", result.CoreWebVitals.TBT)
	}
	if result.CoreWebVitals.SI != 3000 {
		t.Errorf("CWV.SI = %v, want 3000", result.CoreWebVitals.SI)
	}
	if result.CoreWebVitals.TTFB != 120 {
		t.Errorf("CWV.TTFB = %v, want 120", result.CoreWebVitals.TTFB)
	}

	// Verify audits
	if len(result.Audits) != 6 {
		t.Errorf("Audits count = %d, want 6", len(result.Audits))
	}
	lcp, ok := result.Audits["largest-contentful-paint"]
	if !ok {
		t.Fatal("missing largest-contentful-paint audit")
	}
	if lcp.Score != 0.75 {
		t.Errorf("LCP audit score = %v, want 0.75", lcp.Score)
	}
	if lcp.DisplayValue != "2.5 s" {
		t.Errorf("LCP display = %q, want %q", lcp.DisplayValue, "2.5 s")
	}
}

func TestParsePSIResponse_Empty(t *testing.T) {
	raw := map[string]interface{}{}
	result := ParsePSIResponse(raw, "https://example.com", "desktop")

	if result.URL != "https://example.com" {
		t.Errorf("URL = %q, want %q", result.URL, "https://example.com")
	}
	if result.PerformanceScore != 0 {
		t.Errorf("PerformanceScore = %v, want 0", result.PerformanceScore)
	}
	if result.Audits != nil {
		t.Errorf("Audits should be nil for empty response, got %v", result.Audits)
	}
}
