package urlutil

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// ScopeChecker determines whether a URL is within the configured crawl boundary.
type ScopeChecker struct {
	mode         string
	seedHost     string
	seedDomain   string
	allowedHosts map[string]bool
}

// NewScopeChecker creates a scope checker for the given mode.
// Supported modes: "registrable_domain", "exact_host", "allowlist".
func NewScopeChecker(mode string, seedHost string, allowedHosts []string) (*ScopeChecker, error) {
	sc := &ScopeChecker{
		mode:     mode,
		seedHost: strings.ToLower(seedHost),
	}

	switch mode {
	case "registrable_domain":
		if seedHost == "" {
			return nil, fmt.Errorf("seedHost is required for %q mode", mode)
		}
		domain, err := publicsuffix.EffectiveTLDPlusOne(sc.seedHost)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve eTLD+1 for %q: %w", seedHost, err)
		}
		sc.seedDomain = domain

	case "exact_host":
		if seedHost == "" {
			return nil, fmt.Errorf("seedHost is required for %q mode", mode)
		}
		// seedHost already lowercased

	case "allowlist":
		sc.allowedHosts = make(map[string]bool, len(allowedHosts))
		for _, h := range allowedHosts {
			sc.allowedHosts[strings.ToLower(h)] = true
		}

	default:
		return nil, fmt.Errorf("unknown scope mode %q", mode)
	}

	return sc, nil
}

// IsInScope returns true if the URL is within the configured crawl boundary.
func (sc *ScopeChecker) IsInScope(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}

	// Only allow HTTP(S) URLs through scope check.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	// Reject URLs with userinfo (potential scope confusion).
	if parsed.User != nil {
		return false
	}

	host := strings.ToLower(parsed.Hostname())

	switch sc.mode {
	case "registrable_domain":
		domain, err := publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil {
			return false
		}
		return domain == sc.seedDomain

	case "exact_host":
		return host == sc.seedHost

	case "allowlist":
		return sc.allowedHosts[host]

	default:
		return false
	}
}
