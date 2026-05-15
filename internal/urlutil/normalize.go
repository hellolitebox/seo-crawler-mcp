// Package urlutil provides URL normalization and utility functions
// for the SEO crawler, implementing RFC 3986 canonicalization rules.
package urlutil

import (
	"fmt"
	"net/url"
	"strings"
)

// hexDigits is the lookup table for uppercase hex encoding.
const hexDigits = "0123456789ABCDEF"

// droppedSchemes are schemes we reject or drop entirely.
var droppedSchemes = map[string]bool{
	"ftp":        true,
	"mailto":     true,
	"javascript": true,
	"tel":        true,
	"data":       true,
	"file":       true,
	"gopher":     true,
	"dict":       true,
	"ldap":       true,
	"ldaps":      true,
	"sftp":       true,
}

// defaultPorts maps scheme to its default port string.
var defaultPorts = map[string]string{
	"http":  "80",
	"https": "443",
}

// isUnreserved returns true if b is an unreserved character per RFC 3986 §2.3.
func isUnreserved(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '-', b == '.', b == '_', b == '~':
		return true
	}
	return false
}

// hexVal returns the numeric value of a hex digit, or -1.
func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		return int(c - 'A' + 10)
	}
	return -1
}

// normalizePercentEncoding processes percent-encoding in a string.
// If decodeUnreserved is true, unreserved chars are decoded (for path).
// Always uppercases hex digits in remaining percent-encoded triplets.
func normalizePercentEncoding(s string, decodeUnreserved bool) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi := hexVal(s[i+1])
			lo := hexVal(s[i+2])
			if hi >= 0 && lo >= 0 {
				decoded := byte(hi<<4 | lo)
				if decodeUnreserved && isUnreserved(decoded) {
					b.WriteByte(decoded)
				} else {
					b.WriteByte('%')
					b.WriteByte(hexDigits[decoded>>4])
					b.WriteByte(hexDigits[decoded&0x0F])
				}
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}

	return b.String()
}

// sanitizeControlChars strips tab, newline, carriage return, and null bytes
// from a URL string. These characters are silently stripped by url.Parse
// and could bypass string prefix checks if not removed first.
func sanitizeControlChars(s string) string {
	// Fast path: most URLs have no control chars.
	clean := true
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == 0 {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\t' && c != '\n' && c != '\r' && c != 0 {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Normalize canonicalizes a URL per RFC 3986:
//   - strips fragments
//   - lowercases scheme and host
//   - drops default ports (:80 for http, :443 for https)
//   - normalizes empty path to "/"
//   - decodes percent-encoded unreserved characters in path
//   - uppercases remaining percent-encoding hex digits
//   - rejects non-http(s) schemes
func Normalize(rawURL string) (string, error) {
	rawURL = sanitizeControlChars(rawURL)

	// Check for dropped schemes before parsing (some don't parse well).
	if IsDroppedScheme(rawURL) {
		if idx := strings.Index(rawURL, ":"); idx > 0 {
			return "", fmt.Errorf("unsupported scheme %q", strings.ToLower(rawURL[:idx]))
		}
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		return "", fmt.Errorf("invalid URL %q: missing scheme", rawURL)
	}

	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", scheme)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("invalid URL %q: missing host", rawURL)
	}

	// Drop default port.
	port := u.Port()
	if port != "" && port != defaultPorts[scheme] {
		host = host + ":" + port
	}

	// Normalize path.
	path := u.RawPath
	if path == "" {
		path = u.Path
	}
	if path == "" {
		path = "/"
	}

	// Percent-encoding normalization on path: decode unreserved, uppercase hex.
	path = normalizePercentEncoding(path, true)

	// Query: only uppercase hex digits, do NOT decode unreserved.
	rawQuery := u.RawQuery
	if rawQuery != "" {
		rawQuery = normalizePercentEncoding(rawQuery, false)
	}

	// Rebuild URL without fragment.
	var b strings.Builder
	b.Grow(len(scheme) + 3 + len(host) + len(path) + 1 + len(rawQuery))
	b.WriteString(scheme)
	b.WriteString("://")
	b.WriteString(host)
	b.WriteString(path)
	if rawQuery != "" {
		b.WriteByte('?')
		b.WriteString(rawQuery)
	}

	return b.String(), nil
}

// FrontierKey normalizes a URL and strips ignored query parameters for
// frontier deduplication. Supports exact param names and wildcard suffix
// patterns like "utm_*". Splits query manually to preserve original param
// order (url.Values.Encode() reorders alphabetically). Use ONLY for
// frontier admission, not stored URL identity.
func FrontierKey(rawURL string, ignoreParams []string) (string, error) {
	normalized, err := Normalize(rawURL)
	if err != nil {
		return "", err
	}

	if len(ignoreParams) == 0 {
		return normalized, nil
	}

	u, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}

	if u.RawQuery == "" {
		return normalized, nil
	}

	// Parse query preserving original order by manual split.
	pairs := strings.Split(u.RawQuery, "&")
	kept := make([]string, 0, len(pairs))

	for _, pair := range pairs {
		key := pair
		if idx := strings.IndexByte(pair, '='); idx >= 0 {
			key = pair[:idx]
		}
		if shouldIgnoreParam(key, ignoreParams) {
			continue
		}
		kept = append(kept, pair)
	}

	// Rebuild.
	var b strings.Builder
	b.WriteString(u.Scheme)
	b.WriteString("://")
	b.WriteString(u.Host)
	b.WriteString(u.Path)
	if len(kept) > 0 {
		b.WriteByte('?')
		b.WriteString(strings.Join(kept, "&"))
	}

	return b.String(), nil
}

// shouldIgnoreParam checks if a param key matches any ignore pattern.
func shouldIgnoreParam(key string, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasSuffix(p, "*") {
			prefix := p[:len(p)-1]
			if strings.HasPrefix(key, prefix) {
				return true
			}
		} else if key == p {
			return true
		}
	}
	return false
}

// HasRepeatedPathSegments returns true if the URL path contains 3 or more
// consecutive identical non-empty segments, indicating a likely crawl trap.
func HasRepeatedPathSegments(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	segments := strings.Split(u.Path, "/")

	count := 1
	for i := 1; i < len(segments); i++ {
		if segments[i] == "" {
			count = 1
			continue
		}
		if segments[i] == segments[i-1] {
			count++
			if count >= 3 {
				return true
			}
		} else {
			count = 1
		}
	}

	return false
}

// IsDroppedScheme returns true for URL schemes the crawler should ignore:
// mailto, javascript, tel, data, ftp.
func IsDroppedScheme(rawURL string) bool {
	// Only need to check the scheme prefix, not the entire URL.
	maxSchemeLen := 12 // "javascript:" is the longest at 11 chars
	prefix := rawURL
	if len(prefix) > maxSchemeLen {
		prefix = prefix[:maxSchemeLen]
	}
	prefix = sanitizeControlChars(prefix)
	lower := strings.ToLower(prefix)
	for scheme := range droppedSchemes {
		if strings.HasPrefix(lower, scheme+":") {
			return true
		}
	}
	return false
}

// ResolveReference resolves a possibly relative reference against a base URL.
// Returns the resolved URL string, or empty string on error.
func ResolveReference(baseURL, ref string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	rel, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return base.ResolveReference(rel).String()
}
