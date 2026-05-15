// Package encoding provides character encoding detection and UTF-8 conversion.
package encoding

import (
	"bytes"
	"fmt"
	"mime"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

var (
	metaCharsetRe    = regexp.MustCompile(`(?i)<meta[^>]+charset=["']?([^"'\s;>]+)`)
	metaHTTPEquivRe  = regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']?Content-Type["']?[^>]+content=["']?[^"']*charset=([^"'\s;>]+)`)
	metaHTTPEquivRe2 = regexp.MustCompile(`(?i)<meta[^>]+content=["']?[^"']*charset=([^"'\s;>]+)[^>]+http-equiv=["']?Content-Type["']?`)
)

// DetectAndConvert detects charset from Content-Type header and HTML meta tags,
// then converts body to UTF-8 if needed.
func DetectAndConvert(body []byte, contentTypeHeader string) ([]byte, error) {
	charset := charsetFromHeader(contentTypeHeader)
	if charset == "" {
		charset = charsetFromHTML(body)
	}
	if charset == "" || isUTF8(charset) {
		return body, nil
	}

	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("unsupported charset %q: %w", charset, err)
	}

	decoder := enc.NewDecoder()
	result, _, err := transform.Bytes(decoder, body)
	if err != nil {
		return nil, fmt.Errorf("decoding charset %q: %w", charset, err)
	}
	return result, nil
}

func charsetFromHeader(contentType string) string {
	if contentType == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["charset"])
}

func charsetFromHTML(body []byte) string {
	// Only scan first 1024 bytes for meta tags (per HTML spec recommendation).
	snippet := body
	if len(snippet) > 1024 {
		snippet = snippet[:1024]
	}

	if m := metaCharsetRe.FindSubmatch(snippet); m != nil {
		return string(bytes.TrimSpace(m[1]))
	}
	if m := metaHTTPEquivRe.FindSubmatch(snippet); m != nil {
		return string(bytes.TrimSpace(m[1]))
	}
	if m := metaHTTPEquivRe2.FindSubmatch(snippet); m != nil {
		return string(bytes.TrimSpace(m[1]))
	}
	return ""
}

func isUTF8(charset string) bool {
	c := strings.ToLower(strings.TrimSpace(charset))
	return c == "utf-8" || c == "utf8"
}
