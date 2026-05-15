// Package robots implements a robots.txt parser with user-agent matching,
// wildcard/anchor pattern support, and crawl-delay extraction.
package robots

import (
	"strconv"
	"strings"
)

// Directive represents a single robots.txt directive.
type Directive struct {
	UserAgent   string
	RuleType    string // Allow, Disallow, Crawl-delay, Sitemap
	PathPattern string
}

// rule is an internal representation of an allow/disallow rule.
type rule struct {
	ruleType string // "allow" or "disallow"
	pattern  string
}

// agentGroup holds the rules and crawl-delay for a single user-agent group.
type agentGroup struct {
	rules      []rule
	crawlDelay int
}

// RobotsFile is the parsed representation of a robots.txt file.
type RobotsFile struct {
	Rules    []Directive
	Sitemaps []string
	groups   map[string]*agentGroup
}

// Parse parses a robots.txt content string into a RobotsFile.
func Parse(content string) (*RobotsFile, error) {
	rf := &RobotsFile{
		Rules:    make([]Directive, 0),
		Sitemaps: make([]string, 0),
		groups:   make(map[string]*agentGroup),
	}

	if strings.TrimSpace(content) == "" {
		return rf, nil
	}

	lines := strings.Split(content, "\n")
	currentAgents := make([]string, 0)
	var currentGroup *agentGroup
	hadRule := false

	for _, rawLine := range lines {
		// Strip comments.
		if idx := strings.Index(rawLine, "#"); idx >= 0 {
			rawLine = rawLine[:idx]
		}
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}

		field := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])
		fieldLower := strings.ToLower(field)

		switch fieldLower {
		case "user-agent":
			ua := strings.ToLower(value)
			if hadRule {
				// New group starts after rules.
				currentAgents = make([]string, 0)
				currentGroup = nil
				hadRule = false
			}
			if currentGroup == nil {
				currentGroup = &agentGroup{
					rules: make([]rule, 0),
				}
			}
			currentAgents = append(currentAgents, ua)
			rf.groups[ua] = currentGroup

		case "disallow":
			if currentGroup == nil {
				continue
			}
			hadRule = true
			if value == "" {
				// Empty Disallow = allow all (no rule added).
				continue
			}
			currentGroup.rules = append(currentGroup.rules, rule{ruleType: "disallow", pattern: value})
			for _, ua := range currentAgents {
				rf.Rules = append(rf.Rules, Directive{UserAgent: ua, RuleType: "Disallow", PathPattern: value})
			}

		case "allow":
			if currentGroup == nil {
				continue
			}
			hadRule = true
			currentGroup.rules = append(currentGroup.rules, rule{ruleType: "allow", pattern: value})
			for _, ua := range currentAgents {
				rf.Rules = append(rf.Rules, Directive{UserAgent: ua, RuleType: "Allow", PathPattern: value})
			}

		case "crawl-delay":
			if currentGroup == nil {
				continue
			}
			hadRule = true
			delay, err := strconv.Atoi(value)
			if err == nil {
				currentGroup.crawlDelay = delay
			}
			for _, ua := range currentAgents {
				rf.Rules = append(rf.Rules, Directive{UserAgent: ua, RuleType: "Crawl-delay", PathPattern: value})
			}

		case "sitemap":
			rf.Sitemaps = append(rf.Sitemaps, value)
			rf.Rules = append(rf.Rules, Directive{RuleType: "Sitemap", PathPattern: value})
		}
	}

	return rf, nil
}

// IsAllowed checks whether the given user-agent is allowed to access the path.
func (rf *RobotsFile) IsAllowed(ua, path string) bool {
	group := rf.findGroup(ua)
	if group == nil {
		return true
	}
	if len(group.rules) == 0 {
		return true
	}

	// Find the best (longest) matching rule.
	var bestRule *rule
	bestLen := -1

	for i := range group.rules {
		r := &group.rules[i]
		patLen := patternLength(r.pattern)
		if !matchPattern(path, r.pattern) {
			continue
		}
		if patLen > bestLen || (patLen == bestLen && r.ruleType == "allow") {
			bestRule = r
			bestLen = patLen
		}
	}

	if bestRule == nil {
		return true
	}
	return bestRule.ruleType == "allow"
}

// CrawlDelay returns the crawl-delay in seconds for the given user-agent.
// Returns 0 if no crawl-delay is set.
func (rf *RobotsFile) CrawlDelay(ua string) int {
	group := rf.findGroup(ua)
	if group == nil {
		return 0
	}
	return group.crawlDelay
}

// findGroup looks up the agent group for a UA. Exact match first, then *.
func (rf *RobotsFile) findGroup(ua string) *agentGroup {
	uaLower := strings.ToLower(ua)
	if g, ok := rf.groups[uaLower]; ok {
		return g
	}
	if g, ok := rf.groups["*"]; ok {
		return g
	}
	return nil
}

// patternLength returns the effective length of a pattern for precedence.
// Strips the $ anchor if present.
func patternLength(pattern string) int {
	p := pattern
	if strings.HasSuffix(p, "$") {
		p = p[:len(p)-1]
	}
	return len(p)
}

// matchPattern checks if a path matches a robots.txt pattern.
// Supports * wildcards and $ end-of-URL anchors.
func matchPattern(path, pattern string) bool {
	hasAnchor := strings.HasSuffix(pattern, "$")
	p := pattern
	if hasAnchor {
		p = p[:len(p)-1]
	}

	if !strings.Contains(p, "*") {
		// Simple prefix match or exact match with anchor.
		if hasAnchor {
			return path == p
		}
		return strings.HasPrefix(path, p)
	}

	// Wildcard matching: each part between *s must appear in order.
	parts := strings.Split(p, "*")
	remaining := path
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 {
			// First part must be a prefix.
			if !strings.HasPrefix(remaining, part) {
				return false
			}
			remaining = remaining[len(part):]
			continue
		}
		idx := strings.Index(remaining, part)
		if idx < 0 {
			return false
		}
		remaining = remaining[idx+len(part):]
	}

	if hasAnchor {
		return remaining == ""
	}
	return true
}
