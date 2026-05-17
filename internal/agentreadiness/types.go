// Package agentreadiness checks whether a crawled site exposes metadata and
// discovery surfaces useful to AI agents.
package agentreadiness

import (
	"encoding/json"
	"net/http"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

const (
	StatusPass     = "pass"
	StatusWarning  = "warning"
	StatusFail     = "fail"
	StatusOptional = "optional"

	CategoryDiscoverability      = "discoverability"
	CategoryContentAccessibility = "content_accessibility"
	CategoryBotAccessControl     = "bot_access_control"
	CategoryProtocolDiscovery    = "protocol_discovery"
	CategoryCommerce             = "commerce"
)

// ResourceLink points users to implementation references for a check.
type ResourceLink struct {
	Label string
	URL   string
}

// CheckResult is the normalized result emitted by every agent-readiness check.
type CheckResult struct {
	Category        string
	CheckKey        string
	Status          string
	Score           int
	TargetURL       string
	Endpoint        string
	Method          string
	RequestHeaders  http.Header
	ResponseStatus  *int
	ResponseHeaders http.Header
	Evidence        map[string]any
	Recommendation  string
	Resources       []ResourceLink
}

// ToStorageInput converts a normalized check result into a storage input.
func (r CheckResult) ToStorageInput(jobID string) storage.AgentReadinessCheckInput {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	evidenceJSON := marshalObject(r.Evidence, "{}")
	resourcesJSON := marshalObject(r.Resources, "[]")
	requestHeadersJSON := nullableJSON(r.RequestHeaders)
	responseHeadersJSON := nullableJSON(r.ResponseHeaders)
	recommendation := nullableString(r.Recommendation)

	return storage.AgentReadinessCheckInput{
		JobID:               jobID,
		Category:            r.Category,
		CheckKey:            r.CheckKey,
		Status:              r.Status,
		Score:               r.Score,
		TargetURL:           r.TargetURL,
		Endpoint:            r.Endpoint,
		Method:              method,
		RequestHeadersJSON:  requestHeadersJSON,
		ResponseStatus:      r.ResponseStatus,
		ResponseHeadersJSON: responseHeadersJSON,
		EvidenceJSON:        evidenceJSON,
		Recommendation:      recommendation,
		ResourcesJSON:       resourcesJSON,
	}
}

func marshalObject(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

func nullableJSON(v http.Header) *string {
	if len(v) == 0 {
		return nil
	}
	s := marshalObject(v, "{}")
	return &s
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
