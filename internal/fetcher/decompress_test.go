package fetcher

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestDecompressGzip(t *testing.T) {
	original := []byte("hello gzip world")
	compressed := gzipBytes(t, original)

	r, err := decompressAndLimit(bytes.NewReader(compressed), "gzip", 1024)
	if err != nil {
		t.Fatalf("decompressAndLimit returned error: %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, original) {
		t.Errorf("got %q, want %q", got, original)
	}
}

func TestDecompressIdentity(t *testing.T) {
	original := []byte("plain text body")

	r, err := decompressAndLimit(bytes.NewReader(original), "", 1024)
	if err != nil {
		t.Fatalf("decompressAndLimit returned error: %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, original) {
		t.Errorf("got %q, want %q", got, original)
	}
}

func TestDecompressUnknown(t *testing.T) {
	original := []byte("some bytes that are not actually brotli")

	r, err := decompressAndLimit(bytes.NewReader(original), "br", 1024)
	if err != nil {
		t.Fatalf("decompressAndLimit returned error: %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, original) {
		t.Errorf("got %q, want %q", got, original)
	}
}
