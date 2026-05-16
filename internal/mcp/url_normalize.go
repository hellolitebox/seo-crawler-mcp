package mcp

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func normalizeToolURL(rawURL string) (string, *url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", nil, fmt.Errorf("url is required")
	}

	if !strings.Contains(trimmed, "://") {
		if looksLikeUnsupportedURLScheme(trimmed) {
			return "", nil, fmt.Errorf("invalid URL %q: enter a domain or http(s) URL", rawURL)
		}
		trimmed = "https://" + strings.TrimLeft(trimmed, "/")
	}

	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", nil, fmt.Errorf("invalid URL %q: enter a domain or http(s) URL", rawURL)
	}
	return trimmed, parsed, nil
}

func looksLikeUnsupportedURLScheme(value string) bool {
	colon := strings.Index(value, ":")
	if colon <= 0 {
		return false
	}
	if sep := strings.IndexAny(value, "/?#"); sep >= 0 && sep < colon {
		return false
	}

	hostPart := value[:colon]
	rest := value[colon+1:]
	if sep := strings.IndexAny(rest, "/?#"); sep >= 0 {
		rest = rest[:sep]
	}
	if (strings.Contains(hostPart, ".") || strings.EqualFold(hostPart, "localhost")) && rest != "" {
		if _, err := strconv.Atoi(rest); err == nil {
			return false
		}
	}
	return true
}
