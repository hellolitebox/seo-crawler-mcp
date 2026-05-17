package agentreadiness

import (
	"encoding/json"
	"net/http"
)

type HTTPProbe struct {
	URL         string
	Endpoint    string
	Status      *int
	Headers     http.Header
	BodyPreview string
	Error       string
}

type endpointDefinition struct {
	Category       string
	CheckKey       string
	Endpoint       string
	Accept         string
	RequiredFields []string
	Recommendation string
	Resources      []ResourceLink
	Optional       bool
}

var singleEndpointChecks = []endpointDefinition{
	{Category: CategoryBotAccessControl, CheckKey: "web_bot_auth", Endpoint: "/.well-known/http-message-signatures-directory", RequiredFields: []string{}, Recommendation: "Publish a Web Bot Auth HTTP message signatures directory if you require signed bot requests.", Resources: []ResourceLink{{Label: "Web Bot Auth", URL: "https://blog.cloudflare.com/web-bot-auth/"}}},
	{Category: CategoryProtocolDiscovery, CheckKey: "api_catalog", Endpoint: "/.well-known/api-catalog", Accept: "application/linkset+json, application/json", RequiredFields: []string{"linkset"}, Recommendation: "Publish an API Catalog at /.well-known/api-catalog with service-desc, service-doc, and status relations.", Resources: []ResourceLink{{Label: "RFC 9727", URL: "https://www.rfc-editor.org/rfc/rfc9727"}}},
	{Category: CategoryProtocolDiscovery, CheckKey: "oauth_protected_resource", Endpoint: "/.well-known/oauth-protected-resource", Accept: "application/json", RequiredFields: []string{"authorization_servers"}, Recommendation: "Publish OAuth Protected Resource metadata so agents can discover how to obtain access tokens.", Resources: []ResourceLink{{Label: "RFC 9728", URL: "https://www.rfc-editor.org/rfc/rfc9728"}}},
	{Category: CategoryCommerce, CheckKey: "ucp", Endpoint: "/.well-known/ucp", Accept: "application/json", RequiredFields: []string{}, Recommendation: "Publish a UCP profile only if the site supports agentic commerce.", Optional: true},
	{Category: CategoryCommerce, CheckKey: "acp", Endpoint: "/.well-known/acp.json", Accept: "application/json", RequiredFields: []string{}, Recommendation: "Publish an ACP discovery document only if the site supports agentic commerce.", Optional: true},
	{Category: CategoryCommerce, CheckKey: "mpp", Endpoint: "/openapi.json", Accept: "application/json", RequiredFields: []string{}, Recommendation: "Expose machine-payment metadata in OpenAPI only if the API supports machine payments.", Optional: true},
}

var multiEndpointChecks = map[string][]endpointDefinition{
	"oauth_oidc_discovery": {
		{Category: CategoryProtocolDiscovery, CheckKey: "oauth_oidc_discovery", Endpoint: "/.well-known/openid-configuration", Accept: "application/json", RequiredFields: []string{"issuer"}},
		{Category: CategoryProtocolDiscovery, CheckKey: "oauth_oidc_discovery", Endpoint: "/.well-known/oauth-authorization-server", Accept: "application/json", RequiredFields: []string{"issuer"}},
	},
	"mcp_server_card": {
		{Category: CategoryProtocolDiscovery, CheckKey: "mcp_server_card", Endpoint: "/.well-known/mcp/server-cards.json", Accept: "application/json", RequiredFields: []string{"server"}},
		{Category: CategoryProtocolDiscovery, CheckKey: "mcp_server_card", Endpoint: "/.well-known/mcp.json", Accept: "application/json", RequiredFields: []string{"server"}},
		{Category: CategoryProtocolDiscovery, CheckKey: "mcp_server_card", Endpoint: "/.well-known/mcp/server-card.json", Accept: "application/json", RequiredFields: []string{"server"}},
	},
	"agent_skills": {
		{Category: CategoryProtocolDiscovery, CheckKey: "agent_skills", Endpoint: "/.well-known/agent-skills/index.json", Accept: "application/json", RequiredFields: []string{"skills"}},
		{Category: CategoryProtocolDiscovery, CheckKey: "agent_skills", Endpoint: "/.well-known/skills/index.json", Accept: "application/json", RequiredFields: []string{"skills"}},
	},
}

func evaluateEndpoint(targetURL string, def endpointDefinition, probe HTTPProbe) CheckResult {
	status := StatusFail
	score := 0
	if def.Optional {
		status = StatusOptional
	}
	if probe.Status != nil && *probe.Status >= 200 && *probe.Status < 300 && bodyHasFields(probe.BodyPreview, def.RequiredFields) {
		status = StatusPass
		score = 100
	}
	rec := def.Recommendation
	if status == StatusPass {
		rec = ""
	}
	return CheckResult{
		Category:        def.Category,
		CheckKey:        def.CheckKey,
		Status:          status,
		Score:           score,
		TargetURL:       targetURL,
		Endpoint:        def.Endpoint,
		Method:          http.MethodGet,
		RequestHeaders:  acceptHeader(def.Accept),
		ResponseStatus:  probe.Status,
		ResponseHeaders: probe.Headers,
		Evidence:        map[string]any{"probe": probe, "requiredFields": def.RequiredFields},
		Recommendation:  rec,
		Resources:       def.Resources,
	}
}

func evaluateMultiEndpoint(targetURL, checkKey string, defs []endpointDefinition, probes []HTTPProbe) CheckResult {
	category := defs[0].Category
	status := StatusFail
	score := 0
	endpoint := defs[0].Endpoint
	var responseStatus *int
	var responseHeaders http.Header
	for i, probe := range probes {
		if probe.Status != nil && *probe.Status >= 200 && *probe.Status < 300 && bodyHasFields(probe.BodyPreview, defs[i].RequiredFields) {
			status = StatusPass
			score = 100
			endpoint = defs[i].Endpoint
			responseStatus = probe.Status
			responseHeaders = probe.Headers
			break
		}
	}
	rec := recommendationForMulti(checkKey)
	if status == StatusPass {
		rec = ""
	}
	if responseStatus == nil && len(probes) > 0 {
		responseStatus = probes[0].Status
		responseHeaders = probes[0].Headers
	}
	return CheckResult{Category: category, CheckKey: checkKey, Status: status, Score: score, TargetURL: targetURL, Endpoint: endpoint, Method: http.MethodGet, ResponseStatus: responseStatus, ResponseHeaders: responseHeaders, Evidence: map[string]any{"probes": probes}, Recommendation: rec, Resources: resourcesForMulti(checkKey)}
}

func bodyHasFields(body string, fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return false
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return false
	}
	for _, field := range fields {
		if _, ok := obj[field]; !ok {
			return false
		}
	}
	return true
}

func acceptHeader(value string) http.Header {
	if value == "" {
		return nil
	}
	return http.Header{"Accept": []string{value}}
}

func recommendationForMulti(checkKey string) string {
	switch checkKey {
	case "oauth_oidc_discovery":
		return "Publish OAuth/OIDC discovery metadata so agents can authenticate with protected APIs."
	case "mcp_server_card":
		return "Publish an MCP Server Card under /.well-known so agents can discover available MCP transport and capabilities."
	case "agent_skills":
		return "Publish an Agent Skills index with skill names, descriptions, URLs, and digests."
	default:
		return "Publish the relevant well-known discovery metadata."
	}
}

func resourcesForMulti(checkKey string) []ResourceLink {
	switch checkKey {
	case "oauth_oidc_discovery":
		return []ResourceLink{{Label: "RFC 8414", URL: "https://www.rfc-editor.org/rfc/rfc8414"}, {Label: "OpenID Connect Discovery", URL: "https://openid.net/specs/openid-connect-discovery-1_0.html"}}
	case "mcp_server_card":
		return []ResourceLink{{Label: "Model Context Protocol", URL: "https://modelcontextprotocol.io/"}}
	case "agent_skills":
		return []ResourceLink{{Label: "Agent Skills", URL: "https://agentskills.io/home"}}
	default:
		return nil
	}
}
