package urlutil

import "testing"

func TestScopeChecker_RegistrableDomain(t *testing.T) {
	sc, err := NewScopeChecker("registrable_domain", "www.example.com", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "same host", url: "https://www.example.com/page", want: true},
		{name: "subdomain included", url: "https://blog.example.com/page", want: true},
		{name: "apex included", url: "https://example.com/page", want: true},
		{name: "different domain excluded", url: "https://other.com/page", want: false},
		{name: "invalid URL returns false", url: "://bad", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sc.IsInScope(tt.url)
			if got != tt.want {
				t.Errorf("IsInScope(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestScopeChecker_ExactHost(t *testing.T) {
	sc, err := NewScopeChecker("exact_host", "www.example.com", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "exact match", url: "https://www.example.com/page", want: true},
		{name: "subdomain excluded", url: "https://blog.example.com/page", want: false},
		{name: "apex excluded", url: "https://example.com/page", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sc.IsInScope(tt.url)
			if got != tt.want {
				t.Errorf("IsInScope(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestScopeChecker_Allowlist(t *testing.T) {
	sc, err := NewScopeChecker("allowlist", "", []string{"example.com", "cdn.example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "allowlist match", url: "https://example.com/page", want: true},
		{name: "allowlist cdn match", url: "https://cdn.example.com/file.js", want: true},
		{name: "allowlist no match", url: "https://other.com/page", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sc.IsInScope(tt.url)
			if got != tt.want {
				t.Errorf("IsInScope(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestScopeChecker_CaseInsensitive(t *testing.T) {
	sc, err := NewScopeChecker("exact_host", "WWW.Example.COM", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !sc.IsInScope("https://www.example.com/page") {
		t.Error("expected case-insensitive match")
	}
}

func TestScopeChecker_SchemeFiltering(t *testing.T) {
	sc, err := NewScopeChecker("registrable_domain", "www.example.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"ftp scheme rejected", "ftp://example.com/file", false},
		{"javascript scheme rejected", "javascript:alert(1)", false},
		{"file scheme rejected", "file:///etc/passwd", false},
		{"http allowed", "http://example.com/page", true},
		{"https allowed", "https://example.com/page", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sc.IsInScope(tt.url)
			if got != tt.want {
				t.Errorf("IsInScope(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestScopeChecker_UserinfoRejection(t *testing.T) {
	sc, err := NewScopeChecker("registrable_domain", "www.example.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"userinfo rejected", "https://user:pass@example.com/page", false},
		{"userinfo bypass rejected", "https://evil.com@example.com/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sc.IsInScope(tt.url)
			if got != tt.want {
				t.Errorf("IsInScope(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestScopeChecker_PortHandling(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		seedHost  string
		url       string
		want      bool
	}{
		{"registrable domain with port", "registrable_domain", "www.example.com", "https://example.com:8443/page", true},
		{"exact host with port", "exact_host", "www.example.com", "https://www.example.com:443/page", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, err := NewScopeChecker(tt.mode, tt.seedHost, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := sc.IsInScope(tt.url)
			if got != tt.want {
				t.Errorf("IsInScope(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestNewScopeChecker_EmptySeedHost(t *testing.T) {
	_, err := NewScopeChecker("registrable_domain", "", nil)
	if err == nil {
		t.Error("expected error for empty seedHost in registrable_domain mode")
	}
	_, err = NewScopeChecker("exact_host", "", nil)
	if err == nil {
		t.Error("expected error for empty seedHost in exact_host mode")
	}
}

func TestNewScopeChecker_InvalidMode(t *testing.T) {
	_, err := NewScopeChecker("invalid_mode", "example.com", []string{})
	if err == nil {
		t.Error("expected error for invalid mode")
	}
}
