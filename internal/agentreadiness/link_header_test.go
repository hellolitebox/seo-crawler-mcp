package agentreadiness

import (
	"net/http"
	"testing"
)

func TestParseLinkHeaderFindsUsefulRelations(t *testing.T) {
	links := ParseLinkHeader([]string{`</.well-known/api-catalog>; rel="api-catalog", </docs/api>; rel="service-doc"`})
	if len(links) != 2 {
		t.Fatalf("links length = %d, want 2", len(links))
	}
	if links[0].URI != "/.well-known/api-catalog" || links[0].Rels[0] != "api-catalog" {
		t.Fatalf("unexpected first link: %+v", links[0])
	}
}

func TestEvaluateLinkHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Add("Link", `</openapi.json>; rel="service-desc"`)
	status := 200
	result := EvaluateLinkHeaders("https://example.com/", headers, &status)
	if result.Status != StatusPass || result.Score != 100 {
		t.Fatalf("result = %+v, want pass/100", result)
	}

	empty := EvaluateLinkHeaders("https://example.com/", http.Header{}, &status)
	if empty.Status != StatusFail || empty.Score != 0 {
		t.Fatalf("empty result = %+v, want fail/0", empty)
	}
}
