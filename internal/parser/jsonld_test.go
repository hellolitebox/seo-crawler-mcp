package parser

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func docFromHTML(html string) *goquery.Document {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		panic(err)
	}
	return doc
}

func TestExtractJSONLD_SingleType(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"@type":"WebPage","name":"Test"}</script></head></html>`
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Malformed {
		t.Fatal("expected valid JSON-LD")
	}
	if len(blocks[0].Types) != 1 || blocks[0].Types[0] != "WebPage" {
		t.Fatalf("expected types=[WebPage], got %v", blocks[0].Types)
	}
}

func TestExtractJSONLD_MultipleBlocks(t *testing.T) {
	html := `<html><head>
		<script type="application/ld+json">{"@type":"WebPage"}</script>
		<script type="application/ld+json">{"@type":"Organization"}</script>
	</head></html>`
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Types[0] != "WebPage" {
		t.Errorf("block 0: expected WebPage, got %v", blocks[0].Types)
	}
	if blocks[1].Types[0] != "Organization" {
		t.Errorf("block 1: expected Organization, got %v", blocks[1].Types)
	}
}

func TestExtractJSONLD_Graph(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"@graph":[{"@type":"WebPage"},{"@type":"BreadcrumbList"}]}</script></head></html>`
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if len(blocks[0].Types) != 2 {
		t.Fatalf("expected 2 types, got %v", blocks[0].Types)
	}
	if blocks[0].Types[0] != "WebPage" || blocks[0].Types[1] != "BreadcrumbList" {
		t.Errorf("unexpected types: %v", blocks[0].Types)
	}
}

func TestExtractJSONLD_NestedTypedObjects(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{
		"@type":"BlogPosting",
		"publisher":{"@type":"Organization","name":"Org"},
		"author":{"@type":"Person","name":"A"}
	}</script></head></html>`
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	want := []string{"BlogPosting", "Organization", "Person"}
	if len(blocks[0].Types) != len(want) {
		t.Fatalf("types = %v, want %v", blocks[0].Types, want)
	}
	got := map[string]bool{}
	for _, typ := range blocks[0].Types {
		got[typ] = true
	}
	for _, typ := range want {
		if !got[typ] {
			t.Fatalf("types = %v, want %v", blocks[0].Types, want)
		}
	}
}

func TestExtractJSONLD_Malformed(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{bad json}</script></head></html>`
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !blocks[0].Malformed {
		t.Fatal("expected Malformed=true")
	}
	if blocks[0].Raw != "{bad json}" {
		t.Errorf("expected raw preserved, got %q", blocks[0].Raw)
	}
	if len(blocks[0].Types) != 0 {
		t.Errorf("expected empty types, got %v", blocks[0].Types)
	}
}

func TestExtractJSONLD_EmptyScript(t *testing.T) {
	html := `<html><head><script type="application/ld+json">  </script></head></html>`
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks for empty script, got %d", len(blocks))
	}
}

func TestExtractJSONLD_TypeAsArray(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"@type":["Article","NewsArticle"]}</script></head></html>`
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if len(blocks[0].Types) != 2 {
		t.Fatalf("expected 2 types, got %v", blocks[0].Types)
	}
	if blocks[0].Types[0] != "Article" || blocks[0].Types[1] != "NewsArticle" {
		t.Errorf("unexpected types: %v", blocks[0].Types)
	}
}

func TestExtractJSONLD_TopLevelArray(t *testing.T) {
	html := "<html><head><script type=\"application/ld+json\">[{\"@type\":\"Article\"},{\"@type\":\"FAQPage\"}]</script></head></html>"
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if len(blocks[0].Types) != 2 {
		t.Fatalf("expected 2 types, got %v", blocks[0].Types)
	}
	if blocks[0].Types[0] != "Article" || blocks[0].Types[1] != "FAQPage" {
		t.Errorf("unexpected types: %v", blocks[0].Types)
	}
}

func TestExtractJSONLD_MIMEVariants(t *testing.T) {
	html := "<html><head><script type=\"Application/LD+JSON; charset=utf-8\">{\"@type\":\"Organization\"}</script></head></html>"
	blocks := ExtractJSONLD(docFromHTML(html))
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if len(blocks[0].Types) != 1 || blocks[0].Types[0] != "Organization" {
		t.Fatalf("expected types=[Organization], got %v", blocks[0].Types)
	}
}
