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
	metaTagRe = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	attrRe    = regexp.MustCompile(`(?is)([a-zA-Z_:][a-zA-Z0-9_:.\-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
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

	for _, tag := range metaTagRe.FindAll(snippet, -1) {
		attrs := parseMetaAttrs(tag)
		if charset := strings.TrimSpace(attrs["charset"]); charset != "" {
			return charset
		}
		if strings.EqualFold(attrs["http-equiv"], "content-type") {
			if charset := charsetFromHeader(attrs["content"]); charset != "" {
				return charset
			}
		}
	}
	return ""
}

func parseMetaAttrs(tag []byte) map[string]string {
	attrs := map[string]string{}
	for _, m := range attrRe.FindAllSubmatch(tag, -1) {
		name := strings.ToLower(string(m[1]))
		value := m[2]
		if len(value) == 0 {
			value = m[3]
		}
		if len(value) == 0 {
			value = m[4]
		}
		attrs[name] = string(bytes.TrimSpace(value))
	}
	return attrs
}

func isUTF8(charset string) bool {
	c := strings.ToLower(strings.TrimSpace(charset))
	return c == "utf-8" || c == "utf8"
}
