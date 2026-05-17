package parser

import (
	"encoding/json"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// JSONLDBlockParsed represents a deeply parsed JSON-LD block with full @type extraction.
type JSONLDBlockParsed struct {
	Raw       string   `json:"raw"`       // raw JSON string
	Types     []string `json:"types"`     // extracted @type values
	Malformed bool     `json:"malformed"` // true if JSON parse failed
}

// ExtractJSONLD extracts all JSON-LD blocks from an HTML document.
// Returns parsed blocks with @type extraction. Tolerates malformed JSON.
func ExtractJSONLD(doc *goquery.Document) []JSONLDBlockParsed {
	blocks := []JSONLDBlockParsed{}

	doc.Find("script[type]").Each(func(_ int, s *goquery.Selection) {
		if !isJSONLDType(s.AttrOr("type", "")) {
			return
		}
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			return
		}

		var parsed interface{} // nosemgrep: go.lang.security.deserialization.unsafe-deserialization-interface.go-unsafe-deserialization-interface
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			blocks = append(blocks, JSONLDBlockParsed{
				Raw:       raw,
				Types:     []string{},
				Malformed: true,
			})
			return
		}

		types := []string{}
		types = extractTypesFromValue(parsed, types)

		blocks = append(blocks, JSONLDBlockParsed{
			Raw:   raw,
			Types: types,
		})
	})

	return blocks
}

func isJSONLDType(scriptType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(scriptType))
	if idx := strings.Index(mediaType, ";"); idx >= 0 {
		mediaType = strings.TrimSpace(mediaType[:idx])
	}
	return mediaType == "application/ld+json"
}

// extractTypesFromValue recursively extracts @type values from a parsed JSON value.
func extractTypesFromValue(v interface{}, types []string) []string {
	obj, ok := v.(map[string]interface{})
	if !ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				types = extractTypesFromValue(item, types)
			}
		}
		return types
	}

	// Extract @type from this object.
	if t, exists := obj["@type"]; exists {
		types = appendTypes(t, types)
	}

	for key, value := range obj {
		if key == "@type" {
			continue
		}
		types = extractTypesFromValue(value, types)
	}

	return types
}

// appendTypes handles @type as either a string or []string.
func appendTypes(v interface{}, types []string) []string {
	switch t := v.(type) {
	case string:
		types = append(types, t)
	case []interface{}:
		for _, item := range t {
			if s, ok := item.(string); ok {
				types = append(types, s)
			}
		}
	}
	return types
}
