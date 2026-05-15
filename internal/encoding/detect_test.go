package encoding

import (
	"testing"
)

func TestUTF8Passthrough(t *testing.T) {
	body := []byte("<html><head><meta charset=\"UTF-8\"></head><body>Hello café</body></html>")
	result, err := DetectAndConvert(body, "text/html; charset=utf-8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("expected passthrough, got different bytes")
	}
}

func TestISO88591Conversion(t *testing.T) {
	// "café" in ISO-8859-1: 0x63 0x61 0x66 0xe9
	iso := []byte{0x63, 0x61, 0x66, 0xe9}
	result, err := DetectAndConvert(iso, "text/html; charset=iso-8859-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "café"
	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestMetaCharsetDetection(t *testing.T) {
	// Body declares latin1 via meta, no header charset.
	iso := append([]byte(`<html><head><meta charset="iso-8859-1"></head><body>`), 0xe9)
	iso = append(iso, []byte(`</body></html>`)...)
	result, err := DetectAndConvert(iso, "text/html")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsUTF8(result, "é") {
		t.Errorf("expected converted UTF-8 é, got %q", string(result))
	}
}

func TestHeaderWinsOverMeta(t *testing.T) {
	// Meta says UTF-8, header says latin1. Header should win.
	iso := append([]byte(`<html><head><meta charset="utf-8"></head><body>`), 0xe9)
	iso = append(iso, []byte(`</body></html>`)...)
	result, err := DetectAndConvert(iso, "text/html; charset=iso-8859-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsUTF8(result, "é") {
		t.Errorf("expected converted UTF-8 é, got %q", string(result))
	}
}

func TestNoCharsetAssumesUTF8(t *testing.T) {
	body := []byte("<html><body>Hello</body></html>")
	result, err := DetectAndConvert(body, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("expected passthrough for missing charset")
	}
}

func TestHTTPEquivMeta(t *testing.T) {
	prefix := `<html><head><meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1"></head><body>`
	iso := append([]byte(prefix), 0xe9)
	iso = append(iso, []byte("</body></html>")...)
	result, err := DetectAndConvert(iso, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsUTF8(result, "é") {
		t.Errorf("expected converted UTF-8 é")
	}
}

func containsUTF8(data []byte, s string) bool {
	for i := range data {
		if i+len(s) <= len(data) && string(data[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}
