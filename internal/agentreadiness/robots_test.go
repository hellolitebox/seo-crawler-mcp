package agentreadiness

import "testing"

func TestDetectAIBotRules(t *testing.T) {
	explicit := DetectAIBotRules("https://example.com/robots.txt", []RobotsRule{{UserAgent: "GPTBot", RuleType: "allow", PathPattern: "/"}})
	if explicit.Status != StatusPass || explicit.Score != 100 {
		t.Fatalf("explicit = %+v, want pass/100", explicit)
	}

	wildcard := DetectAIBotRules("https://example.com/robots.txt", []RobotsRule{{UserAgent: "*", RuleType: "allow", PathPattern: "/"}})
	if wildcard.Status != StatusWarning || wildcard.Score != 50 {
		t.Fatalf("wildcard = %+v, want warning/50", wildcard)
	}
}

func TestParseContentSignals(t *testing.T) {
	signals := ParseContentSignals("User-agent: *\nContent-Signal: ai-train=no, search=yes, ai-input=no\n")
	if len(signals) != 3 {
		t.Fatalf("signals length = %d, want 3", len(signals))
	}
	if signals[0].Key != "ai-train" || signals[0].Value != "no" {
		t.Fatalf("unexpected first signal: %+v", signals[0])
	}
}

func TestEvaluateContentSignalsRequiresKnownSignal(t *testing.T) {
	unknown := EvaluateContentSignals("https://example.com/robots.txt", "Content-Signal: foo=bar\n", nil, nil)
	if unknown.Status == StatusPass {
		t.Fatalf("unknown content signal returned pass: %+v", unknown)
	}

	known := EvaluateContentSignals("https://example.com/robots.txt", "Content-Signal: ai-train=no\n", nil, nil)
	if known.Status != StatusPass || known.Score != 100 {
		t.Fatalf("known content signal = %+v, want pass/100", known)
	}
}
