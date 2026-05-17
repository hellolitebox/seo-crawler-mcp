package agentreadiness

import (
	"net/http"
	"strings"
)

// RobotsRule is the small robots directive shape needed by this package.
type RobotsRule struct {
	UserAgent   string
	RuleType    string
	PathPattern string
}

var knownAIBots = []string{
	"gptbot",
	"chatgpt-user",
	"claudebot",
	"claude-user",
	"perplexitybot",
	"google-extended",
	"applebot-extended",
	"ccbot",
	"anthropic-ai",
	"oai-searchbot",
	"bytespider",
	"meta-externalagent",
	"amazonbot",
	"cohere-ai",
	"youbot",
}

// DetectAIBotRules grades whether robots.txt has explicit AI bot directives.
func DetectAIBotRules(targetURL string, rules []RobotsRule) CheckResult {
	explicit := []RobotsRule{}
	wildcard := []RobotsRule{}
	for _, rule := range rules {
		ua := strings.ToLower(strings.TrimSpace(rule.UserAgent))
		if ua == "*" {
			wildcard = append(wildcard, rule)
			continue
		}
		for _, bot := range knownAIBots {
			if strings.Contains(ua, bot) {
				explicit = append(explicit, rule)
				break
			}
		}
	}

	result := CheckResult{
		Category:  CategoryBotAccessControl,
		CheckKey:  "ai_bot_rules",
		Status:    StatusFail,
		Score:     0,
		TargetURL: targetURL,
		Endpoint:  "/robots.txt",
		Method:    http.MethodGet,
		Evidence: map[string]any{
			"knownBots":     knownAIBots,
			"explicitRules": explicit,
			"wildcardRules": wildcard,
		},
		Recommendation: "Add explicit robots.txt rules for AI crawlers so agent access policy is unambiguous.",
	}
	switch {
	case len(explicit) > 0:
		result.Status = StatusPass
		result.Score = 100
		result.Recommendation = ""
	case len(wildcard) > 0:
		result.Status = StatusWarning
		result.Score = 50
		result.Recommendation = "Wildcard robots.txt rules apply to AI crawlers, but explicit AI bot rules make the policy clearer."
	}
	return result
}

// ContentSignal is one parsed robots.txt Content-Signal directive.
type ContentSignal struct {
	Key   string
	Value string
}

// ParseContentSignals parses Cloudflare-style Content-Signal directives.
func ParseContentSignals(rawRobots string) []ContentSignal {
	signals := []ContentSignal{}
	for _, line := range strings.Split(rawRobots, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Signal") {
			continue
		}
		for _, item := range strings.Split(value, ",") {
			key, val, ok := strings.Cut(strings.TrimSpace(item), "=")
			if !ok {
				continue
			}
			signals = append(signals, ContentSignal{
				Key:   strings.ToLower(strings.TrimSpace(key)),
				Value: strings.ToLower(strings.TrimSpace(val)),
			})
		}
	}
	return signals
}

// EvaluateContentSignals grades whether robots.txt declares AI content usage preferences.
func EvaluateContentSignals(targetURL, rawRobots string, responseStatus *int, responseHeaders http.Header) CheckResult {
	signals := ParseContentSignals(rawRobots)
	result := CheckResult{
		Category:        CategoryBotAccessControl,
		CheckKey:        "content_signals",
		Status:          StatusFail,
		Score:           0,
		TargetURL:       targetURL,
		Endpoint:        "/robots.txt",
		Method:          http.MethodGet,
		ResponseStatus:  responseStatus,
		ResponseHeaders: responseHeaders,
		Evidence: map[string]any{
			"signals": signals,
		},
		Recommendation: "Add Content-Signal directives to robots.txt for ai-train, search, and ai-input preferences.",
		Resources: []ResourceLink{
			{Label: "Content Signals", URL: "https://contentsignals.org/"},
		},
	}
	if len(signals) > 0 {
		result.Status = StatusPass
		result.Score = 100
		result.Recommendation = ""
	}
	return result
}
