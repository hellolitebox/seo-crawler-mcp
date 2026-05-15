package urlutil

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "strip fragment",
			input: "https://example.com/page#section",
			want:  "https://example.com/page",
		},
		{
			name:  "lowercase scheme",
			input: "HTTPS://Example.COM/Path",
			want:  "https://example.com/Path",
		},
		{
			name:  "strip default https port",
			input: "https://example.com:443/path",
			want:  "https://example.com/path",
		},
		{
			name:  "strip default http port",
			input: "http://example.com:80/path",
			want:  "http://example.com/path",
		},
		{
			name:  "keep non-default port",
			input: "https://example.com:8080/path",
			want:  "https://example.com:8080/path",
		},
		{
			name:  "empty path gets slash",
			input: "https://example.com",
			want:  "https://example.com/",
		},
		{
			name:  "preserve trailing slash on path",
			input: "https://example.com/path/",
			want:  "https://example.com/path/",
		},
		{
			name:  "no-slash path stays",
			input: "https://example.com/path",
			want:  "https://example.com/path",
		},
		{
			name:  "http != https",
			input: "http://example.com/",
			want:  "http://example.com/",
		},
		{
			name:  "preserve query",
			input: "https://example.com/search?q=go&page=2",
			want:  "https://example.com/search?q=go&page=2",
		},
		{
			name:  "decode unreserved A",
			input: "https://example.com/%41page",
			want:  "https://example.com/Apage",
		},
		{
			name:  "decode unreserved dash",
			input: "https://example.com/pa%2Dge",
			want:  "https://example.com/pa-ge",
		},
		{
			name:  "decode unreserved tilde",
			input: "https://example.com/%7Euser",
			want:  "https://example.com/~user",
		},
		{
			name:  "uppercase percent hex",
			input: "https://example.com/caf%c3%a9",
			want:  "https://example.com/caf%C3%A9",
		},
		{
			name:  "keep reserved slash encoded",
			input: "https://example.com/a%2Fb",
			want:  "https://example.com/a%2Fb",
		},
		{
			name:  "keep query value encoding",
			input: "https://example.com/?q=%41%42",
			want:  "https://example.com/?q=%41%42",
		},
		{
			name:    "invalid url",
			input:   "://not-a-url",
			wantErr: true,
		},
		{
			name:    "reject ftp",
			input:   "ftp://example.com/",
			wantErr: true,
		},
		{
			name:    "reject javascript",
			input:   "javascript:void(0)",
			wantErr: true,
		},
		{
			name:    "reject mailto",
			input:   "mailto:test@example.com",
			wantErr: true,
		},
		{
			name:    "reject tel",
			input:   "tel:+1234567890",
			wantErr: true,
		},
		{
			name:    "reject data",
			input:   "data:text/html,<h1>hi</h1>",
			wantErr: true,
		},
		{
			name:    "reject file scheme",
			input:   "file:///etc/passwd",
			wantErr: true,
		},
		{
			name:    "tab in scheme bypass",
			input:   "java\tscript:void(0)",
			wantErr: true,
		},
		{
			name:    "newline in scheme bypass",
			input:   "java\nscript:void(0)",
			wantErr: true,
		},
		{
			name:  "uppercase query hex",
			input: "https://example.com/?q=%c3%a9",
			want:  "https://example.com/?q=%C3%A9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Normalize(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("Normalize(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFrontierKey(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		ignoreParams []string
		want         string
	}{
		{
			name:         "no params to strip",
			input:        "https://example.com/page?id=1",
			ignoreParams: []string{},
			want:         "https://example.com/page?id=1",
		},
		{
			name:         "strip utm params",
			input:        "https://example.com/page?id=1&utm_source=google&utm_medium=cpc",
			ignoreParams: []string{"utm_source", "utm_medium"},
			want:         "https://example.com/page?id=1",
		},
		{
			name:         "wildcard match",
			input:        "https://example.com/page?utm_source=x&utm_campaign=y&ref=z",
			ignoreParams: []string{"utm_*"},
			want:         "https://example.com/page?ref=z",
		},
		{
			name:         "strip all",
			input:        "https://example.com/page?fbclid=abc",
			ignoreParams: []string{"fbclid"},
			want:         "https://example.com/page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FrontierKey(tt.input, tt.ignoreParams)
			if err != nil {
				t.Errorf("FrontierKey(%q, %v) unexpected error: %v", tt.input, tt.ignoreParams, err)
				return
			}
			if got != tt.want {
				t.Errorf("FrontierKey(%q, %v) = %q, want %q", tt.input, tt.ignoreParams, got, tt.want)
			}
		})
	}
}

func TestHasRepeatedPathSegments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "no repeat",
			input: "https://example.com/a/b/c",
			want:  false,
		},
		{
			name:  "two repeats",
			input: "https://example.com/a/a/b",
			want:  false,
		},
		{
			name:  "three repeats triggers",
			input: "https://example.com/a/a/a/b",
			want:  true,
		},
		{
			name:  "empty segments",
			input: "https://example.com/",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasRepeatedPathSegments(tt.input)
			if got != tt.want {
				t.Errorf("HasRepeatedPathSegments(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsDroppedScheme(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "mailto", input: "mailto:test@example.com", want: true},
		{name: "javascript", input: "javascript:void(0)", want: true},
		{name: "http", input: "http://example.com", want: false},
		{name: "https", input: "https://example.com", want: false},
		{name: "ftp", input: "ftp://example.com", want: true},
		{name: "tel", input: "tel:+1234567890", want: true},
		{name: "data", input: "data:text/html,<h1>hi</h1>", want: true},
		{name: "file", input: "file:///etc/passwd", want: true},
		{name: "gopher", input: "gopher://evil.com/_GET", want: true},
		{name: "ldap", input: "ldap://internal:389", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDroppedScheme(tt.input)
			if got != tt.want {
				t.Errorf("IsDroppedScheme(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeControlChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no control chars", "https://example.com/", "https://example.com/"},
		{"strip tabs", "https://exam\tple.com/", "https://example.com/"},
		{"strip newlines", "https://exam\nple.com/", "https://example.com/"},
		{"strip null bytes", "https://example\x00.com/", "https://example.com/"},
		{"strip mixed", "ht\t\ntp://example.com/", "http://example.com/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeControlChars(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeControlChars(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
