// Package fetcher provides an HTTP client with SSRF protection, redirect tracking,
// decompression, rate limiting, and TTFB measurement for web crawling.
package fetcher

import (
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// decompressAndLimit wraps the body reader with the appropriate decompressor
// based on Content-Encoding, then applies a size limit to the decompressed output.
// Supported encodings: gzip, x-gzip, deflate, identity, and empty string.
// Unknown encodings (including br) pass through as-is.
func decompressAndLimit(body io.Reader, contentEncoding string, maxDecompressed int64) (io.Reader, error) {
	enc := strings.ToLower(strings.TrimSpace(contentEncoding))

	switch enc {
	case "gzip", "x-gzip":
		gr, err := gzip.NewReader(body)
		if err != nil {
			return nil, fmt.Errorf("fetcher: gzip reader failed: %w", err)
		}
		return io.LimitReader(gr, maxDecompressed), nil

	case "deflate":
		fr := flate.NewReader(body)
		return io.LimitReader(fr, maxDecompressed), nil

	case "", "identity":
		return io.LimitReader(body, maxDecompressed), nil

	default:
		// Unknown encoding (e.g. "br") — pass through as-is with limit.
		return io.LimitReader(body, maxDecompressed), nil
	}
}
