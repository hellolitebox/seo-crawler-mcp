// Package ssrf provides SSRF protection by blocking requests to private and reserved IP ranges.
package ssrf

import (
	"fmt"
	"net"
	"net/url"
)

// Guard provides SSRF protection by blocking private/reserved IPs.
type Guard struct {
	allowPrivateNetworks bool
	blockedCIDRs         []*net.IPNet
	blockedIPs           []net.IP
}

// NewGuard creates an SSRF guard. If allowPrivateNetworks is true, all IP checks are bypassed.
func NewGuard(allowPrivateNetworks bool) *Guard {
	cidrs := []string{
		"0.0.0.0/8",         // "this host" — can alias localhost on some OS
		"127.0.0.0/8",       // loopback
		"10.0.0.0/8",        // private class A
		"100.64.0.0/10",     // carrier-grade NAT (RFC 6598) — internal in cloud
		"172.16.0.0/12",     // private class B
		"192.0.0.0/24",      // IETF protocol assignments
		"192.0.2.0/24",      // TEST-NET-1
		"192.168.0.0/16",    // private class C
		"198.18.0.0/15",     // benchmark testing (RFC 2544)
		"198.51.100.0/24",   // TEST-NET-2
		"203.0.113.0/24",    // TEST-NET-3
		"169.254.0.0/16",    // link-local
		"240.0.0.0/4",       // reserved for future use
		"255.255.255.255/32", // broadcast
		"::1/128",           // IPv6 loopback

		"::/128",            // IPv6 unspecified
		"fe80::/10",         // IPv6 link-local
		"fc00::/7",          // IPv6 unique local
		"2001:db8::/32",     // IPv6 documentation range
	}

	blockedCIDRs := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			// Panic is intentional — CIDRs are hardcoded constants.
			// An invalid CIDR here is a programmer error, not a runtime condition.
			panic(fmt.Sprintf("invalid CIDR %q: %v", cidr, err))
		}
		blockedCIDRs = append(blockedCIDRs, ipNet)
	}

	blockedIPs := []net.IP{
		net.ParseIP("169.254.169.254"),
		net.ParseIP("fd00:ec2::254"),
	}

	return &Guard{
		allowPrivateNetworks: allowPrivateNetworks,
		blockedCIDRs:         blockedCIDRs,
		blockedIPs:           blockedIPs,
	}
}

// IsBlockedIP returns true if the IP is in a blocked range.
func (g *Guard) IsBlockedIP(ip net.IP) bool {
	// Normalize IPv4-mapped IPv6 addresses (e.g., ::ffff:127.0.0.1 → 127.0.0.1)
	// to prevent bypassing IPv4 CIDR blocks via IPv6 notation.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	// Cloud metadata endpoints are ALWAYS blocked, even with allowPrivateNetworks.
	for _, blocked := range g.blockedIPs {
		if blocked.Equal(ip) {
			return true
		}
	}

	if g.allowPrivateNetworks {
		return false
	}

	for _, cidr := range g.blockedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}

// allowedSchemes defines the URL schemes permitted for outbound requests.
var allowedSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

// ValidateURL checks scheme restrictions (http/https only).
func (g *Guard) ValidateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ssrf: invalid URL %q: %w", rawURL, err)
	}

	if !allowedSchemes[parsed.Scheme] {
		return fmt.Errorf("ssrf: scheme %q is not allowed", parsed.Scheme)
	}

	return nil
}

// ValidateHost resolves the hostname via DNS and checks all resolved IPs.
// Also handles raw IP addresses (e.g., http://192.168.1.1/).
//
// IMPORTANT: This validates at call time only. To prevent DNS rebinding attacks
// (where DNS returns a safe IP for validation but a private IP for the actual
// connection), the HTTP client MUST use a custom DialContext that re-validates
// resolved IPs at connection time. See the fetcher package for this integration.
func (g *Guard) ValidateHost(hostname string) error {
	// Check if hostname is a raw IP address.
	if ip := net.ParseIP(hostname); ip != nil {
		if g.IsBlockedIP(ip) {
			return fmt.Errorf("ssrf: IP %s is blocked", ip)
		}
		return nil
	}

	// Resolve hostname via DNS.
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("ssrf: failed to resolve host %q: %w", hostname, err)
	}

	for _, ip := range ips {
		if g.IsBlockedIP(ip) {
			return fmt.Errorf(
				"ssrf: host %q resolved to blocked IP %s",
				hostname, ip,
			)
		}
	}

	return nil
}
