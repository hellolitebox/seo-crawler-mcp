package parser

import (
	"encoding/json"
	"sort"
)

// maxValidationResults caps the number of results to prevent runaway @graph arrays.
const maxValidationResults = 50

// SchemaValidationResult holds the validation outcome for a single JSON-LD object.
type SchemaValidationResult struct {
	Type               string   `json:"type"`
	MissingRequired    []string `json:"missingRequired,omitempty"`
	MissingRecommended []string `json:"missingRecommended,omitempty"`
	Valid              bool     `json:"valid"`
	Nested             bool     `json:"nested"`
	Source             string   `json:"source"`                // "google_rich_results" or "schema_org_best_practice"
	GoogleDocURL       string   `json:"googleDocUrl,omitempty"`
}

// isEmptyValue returns true for nil, empty strings, empty slices, and empty maps.
func isEmptyValue(val interface{}) bool {
	if val == nil {
		return true
	}
	switch v := val.(type) {
	case string:
		return v == ""
	case []interface{}:
		return len(v) == 0
	case map[string]interface{}:
		return len(v) == 0
	}
	return false
}

// ValidateJSONLD validates JSON-LD content against hardcoded Schema.org rules.
// The input can be:
//   - A single JSON object ({"@type": "Article", ...})
//   - A JSON array of objects ([{"@type": "Article"}, ...])
//   - A serialized []JSONLDBlock array ([{"raw": "...", "type": "..."}, ...])
//
// Returns validation results for each object with a known @type.
func ValidateJSONLD(raw string) []SchemaValidationResult {
	if raw == "" {
		return nil
	}

	var results []SchemaValidationResult

	// Try parsing as a JSON array first.
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		for _, item := range arr {
			if len(results) >= maxValidationResults {
				break
			}
			results = append(results, validateRawItem(item, 0)...)
		}
		return results
	}

	// Try as a single object.
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		results = append(results, validateObject(obj, 0)...)
		return results
	}

	// Malformed — return nothing (malformed_structured_data handles that).
	return nil
}

// validateRawItem handles a single raw JSON message which could be a JSONLDBlock
// (with "raw" field) or a direct Schema.org object.
func validateRawItem(data json.RawMessage, depth int) []SchemaValidationResult {
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}

	// Check if this is a JSONLDBlock wrapper (has "raw" field with string content).
	if rawField, ok := obj["raw"]; ok {
		if rawStr, ok := rawField.(string); ok {
			// Check if it also has "malformed" flag.
			if malformed, ok := obj["malformed"]; ok {
				if b, ok := malformed.(bool); ok && b {
					return nil // Skip malformed blocks.
				}
			}
			// Parse the inner raw JSON.
			return validateRawJSON(rawStr, depth)
		}
	}

	// It's a direct Schema.org object.
	return validateObject(obj, depth)
}

// validateRawJSON parses a raw JSON string and validates it.
func validateRawJSON(raw string, depth int) []SchemaValidationResult {
	if raw == "" {
		return nil
	}

	// Could be array or object.
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		var results []SchemaValidationResult
		for _, item := range arr {
			var obj map[string]interface{}
			if err := json.Unmarshal(item, &obj); err == nil {
				results = append(results, validateObject(obj, depth)...)
			}
		}
		return results
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		return validateObject(obj, depth)
	}

	return nil
}

// validateObject validates a single JSON-LD object and recurses into nested objects.
func validateObject(obj map[string]interface{}, depth int) []SchemaValidationResult {
	if depth > 5 {
		return nil
	}

	var results []SchemaValidationResult

	// Handle @graph arrays.
	if graph, ok := obj["@graph"]; ok {
		if arr, ok := graph.([]interface{}); ok {
			for _, item := range arr {
				if len(results) >= maxValidationResults {
					break
				}
				if nested, ok := item.(map[string]interface{}); ok {
					results = append(results, validateObject(nested, depth+1)...)
				}
			}
		}
	}

	// Extract @type(s) from this object.
	types := extractTypes(obj)

	// Validate against each matching rule.
	for _, t := range types {
		rule, ok := schemaRules[t]
		if !ok {
			continue
		}

		var missingRequired, missingRecommended []string

		for _, prop := range rule.Required {
			if v, exists := obj[prop]; !exists || isEmptyValue(v) {
				missingRequired = append(missingRequired, prop)
			}
		}

		for _, prop := range rule.Recommended {
			if v, exists := obj[prop]; !exists || isEmptyValue(v) {
				missingRecommended = append(missingRecommended, prop)
			}
		}

		sort.Strings(missingRequired)
		sort.Strings(missingRecommended)

		results = append(results, SchemaValidationResult{
			Type:               t,
			MissingRequired:    missingRequired,
			MissingRecommended: missingRecommended,
			Valid:              len(missingRequired) == 0,
			Nested:             depth > 0,
			Source:             rule.Source,
			GoogleDocURL:       rule.GoogleDocURL,
		})
	}

	// Recurse into nested objects that have @type.
	for key, val := range obj {
		if key == "@graph" {
			continue // Already handled above.
		}
		if len(results) >= maxValidationResults {
			break
		}
		switch v := val.(type) {
		case map[string]interface{}:
			if _, hasType := v["@type"]; hasType {
				results = append(results, validateObject(v, depth+1)...)
			}
		case []interface{}:
			for _, item := range v {
				if len(results) >= maxValidationResults {
					break
				}
				if nested, ok := item.(map[string]interface{}); ok {
					if _, hasType := nested["@type"]; hasType {
						results = append(results, validateObject(nested, depth+1)...)
					}
				}
			}
		}
	}

	return results
}

// extractTypes gets @type value(s) from a JSON-LD object.
func extractTypes(obj map[string]interface{}) []string {
	t, ok := obj["@type"]
	if !ok {
		return nil
	}

	switch v := t.(type) {
	case string:
		return []string{v}
	case []interface{}:
		var types []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				types = append(types, s)
			}
		}
		return types
	}

	return nil
}
