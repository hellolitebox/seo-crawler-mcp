package ssrf

import (
	"net"
	"testing"
)

func TestIsBlockedIP_Blocked(t *testing.T) {
	g := NewGuard(false)

	tests := []struct {
		name string
		ip   string
	}{
		{name: "localhost v4", ip: "127.0.0.1"},
		{name: "localhost v4 other", ip: "127.0.0.2"},
		{name: "localhost v6", ip: "::1"},
		{name: "private 10.x", ip: "10.0.0.1"},
		{name: "private 172.16.x", ip: "172.16.0.1"},
		{name: "private 192.168.x", ip: "192.168.1.1"},
		{name: "link-local v4", ip: "169.254.1.1"},
		{name: "link-local v6", ip: "fe80::1"},
		{name: "cloud metadata", ip: "169.254.169.254"},
		{name: "ipv4 multicast", ip: "224.0.0.1"},
		{name: "aws ec2 metadata v6", ip: "fd00:ec2::254"},
		{name: "ipv6 multicast", ip: "ff02::1"},
		{name: "zero network", ip: "0.0.0.0"},
		{name: "carrier-grade NAT", ip: "100.64.0.1"},
		{name: "ipv4-mapped ipv6 loopback", ip: "::ffff:127.0.0.1"},
		{name: "ipv4-mapped ipv6 metadata", ip: "::ffff:169.254.169.254"},
		{name: "ipv6 unspecified", ip: "::"},
		{name: "broadcast", ip: "255.255.255.255"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			if !g.IsBlockedIP(ip) {
				t.Errorf("expected %s to be blocked", tt.ip)
			}
		})
	}
}

func TestIsBlockedIP_Allowed(t *testing.T) {
	g := NewGuard(false)

	tests := []struct {
		name string
		ip   string
	}{
		{name: "public IP", ip: "93.184.216.34"},
		{name: "public IP 2", ip: "8.8.8.8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			if g.IsBlockedIP(ip) {
				t.Errorf("expected %s to not be blocked", tt.ip)
			}
		})
	}
}

func TestIsBlockedIP_AllowPrivateNetworks(t *testing.T) {
	g := NewGuard(true)

	ip := net.ParseIP("192.168.1.1")
	if g.IsBlockedIP(ip) {
		t.Error("expected 192.168.1.1 to not be blocked when allowPrivateNetworks=true")
	}
}

func TestValidateURL(t *testing.T) {
	g := NewGuard(false)

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https allowed", url: "https://example.com", wantErr: false},
		{name: "http allowed", url: "http://example.com", wantErr: false},
		{name: "ftp blocked", url: "ftp://example.com", wantErr: true},
		{name: "file blocked", url: "file:///etc/passwd", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error = %v, wantErr = %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateHost_RawIP(t *testing.T) {
	g := NewGuard(false)

	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "public IP", host: "93.184.216.34", wantErr: false},
		{name: "private IP", host: "10.0.0.1", wantErr: true},
		{name: "localhost", host: "127.0.0.1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.ValidateHost(tt.host)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHost(%q) error = %v, wantErr = %v", tt.host, err, tt.wantErr)
			}
		})
	}
}

func TestValidateHost_BlocksDirectPrivateAndMetadataIPs(t *testing.T) {
	g := NewGuard(false)

	for _, host := range []string{
		"10.0.0.1",
		"172.16.0.10",
		"192.168.1.50",
		"169.254.169.254",
		"::ffff:169.254.169.254",
	} {
		t.Run(host, func(t *testing.T) {
			if err := g.ValidateHost(host); err == nil {
				t.Fatalf("ValidateHost(%q) = nil, want blocked", host)
			}
		})
	}
}

func TestIsBlockedIP_CloudMetadataAlwaysBlocked(t *testing.T) {
	g := NewGuard(true) // allowPrivateNetworks=true

	metadataIPs := []string{"169.254.169.254", "fd00:ec2::254"}
	for _, ipStr := range metadataIPs {
		ip := net.ParseIP(ipStr)
		if !g.IsBlockedIP(ip) {
			t.Errorf("cloud metadata IP %s should ALWAYS be blocked, even with allowPrivateNetworks", ipStr)
		}
	}
}

func TestValidateURL_EdgeCases(t *testing.T) {
	g := NewGuard(false)

	cases := []struct {
		name string
		url  string
		err  bool
	}{
		{"empty string", "", true},
		{"data uri", "data:text/html,<h1>hi</h1>", true},
		{"javascript", "javascript:alert(1)", true},
		{"schemeless", "//example.com", true},
		{"relative", "/path/to", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := g.ValidateURL(tt.url)
			if (err != nil) != tt.err {
				t.Errorf("ValidateURL(%q) error=%v, wantErr=%v", tt.url, err, tt.err)
			}
		})
	}
}

func TestValidateHost_DNS(t *testing.T) {
	g := NewGuard(false)

	// "localhost" should resolve to 127.0.0.1 and be blocked
	err := g.ValidateHost("localhost")
	if err == nil {
		t.Error("expected ValidateHost(\"localhost\") to return error")
	}
}
