package agentreadiness

import (
	"net/http"
	"strings"
)

// LinkRelation is one parsed RFC 8288-style Link header value.
type LinkRelation struct {
	URI    string
	Params map[string]string
	Rels   []string
}

var usefulLinkRels = map[string]bool{
	"api-catalog":  true,
	"service-desc": true,
	"service-doc":  true,
	"status":       true,
	"mcp":          true,
	"llms-txt":     true,
}

// ParseLinkHeader parses the subset of RFC 8288 Link headers needed for reporting.
func ParseLinkHeader(values []string) []LinkRelation {
	out := []LinkRelation{}
	for _, value := range values {
		for _, part := range splitHeaderList(value) {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "<") {
				continue
			}
			end := strings.Index(part, ">")
			if end <= 1 {
				continue
			}
			link := LinkRelation{URI: part[1:end], Params: map[string]string{}, Rels: []string{}}
			for _, paramPart := range strings.Split(part[end+1:], ";") {
				paramPart = strings.TrimSpace(paramPart)
				if paramPart == "" {
					continue
				}
				name, raw, ok := strings.Cut(paramPart, "=")
				if !ok {
					continue
				}
				name = strings.ToLower(strings.TrimSpace(name))
				raw = strings.Trim(strings.TrimSpace(raw), "\"")
				link.Params[name] = raw
				if name == "rel" {
					for _, rel := range strings.Fields(raw) {
						link.Rels = append(link.Rels, strings.ToLower(rel))
					}
				}
			}
			out = append(out, link)
		}
	}
	return out
}

func splitHeaderList(value string) []string {
	parts := []string{}
	var b strings.Builder
	inQuote := false
	for _, r := range value {
		switch r {
		case '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case ',':
			if inQuote {
				b.WriteRune(r)
				continue
			}
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// EvaluateLinkHeaders grades homepage Link headers for agent discovery.
func EvaluateLinkHeaders(targetURL string, headers http.Header, statusCode *int) CheckResult {
	values := headers.Values("Link")
	links := ParseLinkHeader(values)
	useful := []LinkRelation{}
	for _, link := range links {
		for _, rel := range link.Rels {
			if usefulLinkRels[rel] {
				useful = append(useful, link)
				break
			}
		}
	}

	result := CheckResult{
		Category:        CategoryDiscoverability,
		CheckKey:        "link_headers",
		Status:          StatusFail,
		Score:           0,
		TargetURL:       targetURL,
		Endpoint:        "/",
		Method:          http.MethodGet,
		ResponseStatus:  statusCode,
		ResponseHeaders: headers,
		Evidence: map[string]any{
			"linkHeaders": values,
			"links":       links,
			"usefulLinks": useful,
		},
		Recommendation: "Add Link response headers on the homepage pointing to API catalogs, service docs, status, MCP, or llms.txt resources.",
		Resources: []ResourceLink{
			{Label: "RFC 8288", URL: "https://www.rfc-editor.org/rfc/rfc8288"},
			{Label: "RFC 9727", URL: "https://www.rfc-editor.org/rfc/rfc9727"},
		},
	}
	if len(useful) > 0 {
		result.Status = StatusPass
		result.Score = 100
		result.Recommendation = ""
	} else if len(links) > 0 {
		result.Status = StatusWarning
		result.Score = 50
	}
	return result
}
