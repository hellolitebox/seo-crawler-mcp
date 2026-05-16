package renderer

import (
	"encoding/json"
	"testing"
)

func TestIsPublicURLRejectsPrivateAndMulticastHosts(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/",
		"http://224.0.0.1/",
		"http://[ff02::1]/",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if IsPublicURL(rawURL) {
				t.Fatalf("IsPublicURL(%q) = true, want false", rawURL)
			}
		})
	}
}

func TestIsPublicURLAcceptsPublicHTTPURLs(t *testing.T) {
	if !IsPublicURL("https://93.184.216.34/") {
		t.Fatal("expected public IPv4 URL to pass")
	}
}

func TestAxeResultPreservesPerPageAuditErrors(t *testing.T) {
	var result AxeResult
	raw := []byte(`{"url":"https://example.com","violations":[],"passes":[],"incomplete":0,"error":"navigation timeout"}`)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal AxeResult: %v", err)
	}
	if result.Error != "navigation timeout" {
		t.Fatalf("Error = %q", result.Error)
	}
}
