package agentreadiness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

const maxProbeBody = 1 << 20

// Runner executes agent-readiness checks for the crawl's seed hosts.
type Runner struct {
	Fetcher   *fetcher.Fetcher
	DB        *storage.DB
	UserAgent string
}

// Run probes agent-readiness surfaces for each distinct seed host.
func (r *Runner) Run(ctx context.Context, jobID string, seedURLs []string) error {
	if r == nil || r.Fetcher == nil || r.DB == nil {
		return nil
	}
	for _, homepage := range uniqueHomepages(seedURLs) {
		if err := r.runForHomepage(ctx, jobID, homepage); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) runForHomepage(ctx context.Context, jobID, homepage string) error {
	r.emit(jobID, "agent_readiness_progress", fmt.Sprintf("checking %s", homepage))
	parsed, err := url.Parse(homepage)
	if err != nil {
		return nil
	}
	host := parsed.Hostname()
	probe := r.probe(ctx, homepage, "/", "")
	if err := r.store(jobID, EvaluateLinkHeaders(homepage, probe.Headers, probe.Status)); err != nil {
		return err
	}

	robotsURL := resolveEndpoint(homepage, "/robots.txt")
	robotsProbe := r.probe(ctx, robotsURL, "/robots.txt", "text/plain, */*")
	rules, _ := r.DB.GetRobotsDirectivesByHost(jobID, host, 10000)
	robotRules := make([]RobotsRule, 0, len(rules))
	for _, rule := range rules {
		robotRules = append(robotRules, RobotsRule{UserAgent: rule.UserAgent, RuleType: rule.RuleType, PathPattern: rule.PathPattern})
	}
	if err := r.store(jobID, DetectAIBotRules(robotsURL, robotRules)); err != nil {
		return err
	}
	if err := r.store(jobID, EvaluateContentSignals(robotsURL, robotsProbe.BodyPreview, robotsProbe.Status, robotsProbe.Headers)); err != nil {
		return err
	}
	if err := r.store(jobID, r.evaluateLlmsTxt(jobID, host, homepage)); err != nil {
		return err
	}

	for _, def := range singleEndpointChecks {
		endpointURL := resolveEndpoint(homepage, def.Endpoint)
		result := evaluateEndpoint(endpointURL, def, r.probe(ctx, endpointURL, def.Endpoint, def.Accept))
		if err := r.store(jobID, result); err != nil {
			return err
		}
	}
	for checkKey, defs := range multiEndpointChecks {
		probes := make([]HTTPProbe, 0, len(defs))
		for _, def := range defs {
			endpointURL := resolveEndpoint(homepage, def.Endpoint)
			probes = append(probes, r.probe(ctx, endpointURL, def.Endpoint, def.Accept))
		}
		if err := r.store(jobID, evaluateMultiEndpoint(resolveEndpoint(homepage, defs[0].Endpoint), checkKey, defs, probes)); err != nil {
			return err
		}
	}
	if err := r.store(jobID, evaluateWebMCP(homepage)); err != nil {
		return err
	}
	return nil
}

func (r *Runner) probe(ctx context.Context, targetURL, endpoint, accept string) HTTPProbe {
	probe := HTTPProbe{URL: targetURL, Endpoint: endpoint}
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	if r.UserAgent != "" {
		req.Header.Set("User-Agent", r.UserAgent)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := r.Fetcher.SafeClient().Do(req)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	probe.Status = &status
	probe.Headers = resp.Header.Clone()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	probe.BodyPreview = string(body)
	return probe
}

func (r *Runner) evaluateLlmsTxt(jobID, host, homepage string) CheckResult {
	target := resolveEndpoint(homepage, "/llms.txt")
	result := CheckResult{Category: CategoryDiscoverability, CheckKey: "llms_txt", Status: StatusFail, Score: 0, TargetURL: target, Endpoint: "/llms.txt", Method: http.MethodGet, Evidence: map[string]any{"present": false}, Recommendation: "Publish /llms.txt to summarize the site for agents.", Resources: []ResourceLink{{Label: "llms.txt", URL: "https://llmstxt.org/"}}}
	finding, err := r.DB.GetLlmsFindingByHost(jobID, host)
	if err == nil && finding != nil && finding.Present {
		result.Status = StatusPass
		result.Score = 100
		result.Recommendation = ""
		result.Evidence = map[string]any{"present": true, "sectionsJson": finding.SectionsJSON.String, "referencedUrlsJson": finding.ReferencedURLsJSON.String}
	}
	return result
}

func evaluateWebMCP(homepage string) CheckResult {
	return CheckResult{Category: CategoryProtocolDiscovery, CheckKey: "webmcp", Status: StatusFail, Score: 0, TargetURL: homepage, Endpoint: "browser:navigator.modelContext", Method: "BROWSER", Evidence: map[string]any{"checked": false, "reason": "browser-level WebMCP detection is not enabled in this backend pass"}, Recommendation: "Expose browser tools through WebMCP only if the site has actions agents should invoke from the page."}
}

func (r *Runner) store(jobID string, result CheckResult) error {
	_, err := r.DB.UpsertAgentReadinessCheck(result.ToStorageInput(jobID))
	return err
}

func (r *Runner) emit(jobID, eventType, message string) {
	details := fmt.Sprintf(`{"message":%q}`, message)
	_, _ = r.DB.InsertEvent(jobID, eventType, &details, nil)
}

func uniqueHomepages(seedURLs []string) []string {
	seen := map[string]bool{}
	homepages := []string{}
	for _, raw := range seedURLs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		homepage := parsed.Scheme + "://" + parsed.Host + "/"
		key := strings.ToLower(homepage)
		if seen[key] {
			continue
		}
		seen[key] = true
		homepages = append(homepages, homepage)
	}
	return homepages
}

func resolveEndpoint(homepage, endpoint string) string {
	base, err := url.Parse(homepage)
	if err != nil {
		return homepage
	}
	ref, err := url.Parse(endpoint)
	if err != nil {
		return homepage
	}
	return base.ResolveReference(ref).String()
}
