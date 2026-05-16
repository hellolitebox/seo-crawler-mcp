package mcp

import "testing"

func TestNormalizeToolURLAcceptsDomainsWithoutScheme(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain domain", in: "pipapou.com", want: "https://pipapou.com"},
		{name: "domain with path", in: "www.pipapou.com/wizard", want: "https://www.pipapou.com/wizard"},
		{name: "domain with port", in: "example.com:8443/path", want: "https://example.com:8443/path"},
		{name: "explicit http", in: "http://example.com", want: "http://example.com"},
		{name: "explicit https", in: "https://example.com/path", want: "https://example.com/path"},
		{name: "trims whitespace", in: "  pipapou.com  ", want: "https://pipapou.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, parsed, err := normalizeToolURL(tt.in)
			if err != nil {
				t.Fatalf("normalizeToolURL(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeToolURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if parsed.Hostname() == "" {
				t.Fatalf("normalized URL %q has empty hostname", got)
			}
		})
	}
}

func TestNormalizeToolURLRejectsUnsupportedSchemes(t *testing.T) {
	for _, rawURL := range []string{"ftp://example.com", "javascript:alert(1)", "mailto:test@example.com"} {
		t.Run(rawURL, func(t *testing.T) {
			if got, _, err := normalizeToolURL(rawURL); err == nil {
				t.Fatalf("normalizeToolURL(%q) = %q, want error", rawURL, got)
			}
		})
	}
}
