// Package parser extracts SEO metadata from HTML documents.
package parser

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/encoding"
)

// RelLink represents a rel=next or rel=prev link.
type RelLink struct {
	Raw      string `json:"raw"`
	Resolved string `json:"resolved"`
}

// HreflangEntry represents a single hreflang alternate link.
type HreflangEntry struct {
	Lang string `json:"lang"`
	URL  string `json:"url"`
}

// HeadingSet holds headings by level.
type HeadingSet struct {
	H1 []string `json:"h1"`
	H2 []string `json:"h2"`
	H3 []string `json:"h3"`
	H4 []string `json:"h4"`
	H5 []string `json:"h5"`
	H6 []string `json:"h6"`
}

// OGTags holds Open Graph metadata.
type OGTags struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	Type        string `json:"type,omitempty"`
	URL         string `json:"url,omitempty"`
}

// TwitterTags holds Twitter Card metadata.
type TwitterTags struct {
	Card        string `json:"card,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
}

// JSONLDBlock holds a single JSON-LD script block.
type JSONLDBlock struct {
	Raw       string `json:"raw"`
	Type      string `json:"type,omitempty"`
	Malformed bool   `json:"malformed,omitempty"`
}

// DiscoveredLink represents a link found in the page.
type DiscoveredLink struct {
	URL        string `json:"url"`
	AnchorText string `json:"anchorText"`
	Rel        string `json:"rel,omitempty"`
	Target     string `json:"target,omitempty"` // e.g. "_blank"
}

// DiscoveredImage represents an image found in the page.
type DiscoveredImage struct {
	Src        string `json:"src"`
	Alt        string `json:"alt"`
	AltEmpty   bool   `json:"altEmpty"`
	AltMissing bool   `json:"altMissing"`
	HasWidth   bool   `json:"hasWidth"`
	HasHeight  bool   `json:"hasHeight"`
}

// DiscoveredAsset represents a non-image asset found in the page (script, stylesheet, etc.).
type DiscoveredAsset struct {
	URL  string `json:"url"`
	Type string `json:"type"` // "script", "stylesheet", "font", "icon", "video", "audio", "preload", "other"
}

// ParseResult holds all extracted SEO data from an HTML page.
type ParseResult struct {
	Title                string
	TitleLength          int
	MetaDescription      string
	DescriptionLength    int
	MetaRobots           string
	XRobotsTag           string
	IndexabilityState    string
	CanonicalRaw         string
	CanonicalResolved    string
	CanonicalType        string // self, cross, absent
	RelNext              *RelLink
	RelPrev              *RelLink
	Hreflangs            []HreflangEntry
	Headings             HeadingSet
	OpenGraph            OGTags
	TwitterCard          TwitterTags
	JSONLDBlocks         []JSONLDBlock
	JSONLDTypes          []string
	Links                []DiscoveredLink
	Images               []DiscoveredImage
	ExtractedWordCount   int
	MainContentWordCount int
	Assets                    []DiscoveredAsset
	ContentHash               string
	JSSuspect                 bool
	ScriptCount               int
	HasSPARoot                bool
	TitleOutsideHead          bool
	MetaRobotsOutsideHead     bool
	TitleCount                int
	DescriptionCount          int
	MetaDescriptionOutsideHead bool
	FirstHeadingLevel         int // level of the first heading encountered (1-6), 0 if none
	H1AltTextOnly             []string // alt texts from H1s that contain only an <img>
	CanonicalCount            int
	CanonicalOutsideHead      bool

	// Medium-priority detectors
	FormInsecureActions       []string // form action URLs starting with "http://"
	ProtocolRelativeCount     int      // count of href/src attributes starting with "//"
	HreflangOutsideHead      bool     // hreflang link tags found in <body>
	InvalidHTMLInHead        []string // non-standard elements found in <head> (div, span, p, etc.)
	HeadTagCount              int      // number of <head> elements
	BodyTagCount              int      // number of <body> elements
	ExtractedText             string   // visible text content (for content checks like lorem ipsum)
	ExtractedTextWithBounds   string   // visible text with block-boundary markers for cross-component detection
}

// ParseHTML extracts SEO metadata from raw HTML bytes.
func ParseHTML(body []byte, pageURL string, responseHeaders http.Header) (*ParseResult, error) {
	utf8Body, err := encoding.DetectAndConvert(body, responseHeaders.Get("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("encoding conversion: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(utf8Body))
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	baseURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("parsing page URL %q: %w", pageURL, err)
	}

	r := &ParseResult{
		Hreflangs:  make([]HreflangEntry, 0),
		JSONLDBlocks: make([]JSONLDBlock, 0),
		JSONLDTypes: make([]string, 0),
		Links:      make([]DiscoveredLink, 0),
		Images:     make([]DiscoveredImage, 0),
		Assets:     make([]DiscoveredAsset, 0),
	}
	r.Headings = HeadingSet{
		H1: make([]string, 0),
		H2: make([]string, 0),
		H3: make([]string, 0),
		H4: make([]string, 0),
		H5: make([]string, 0),
		H6: make([]string, 0),
	}

	// Title
	r.Title = strings.TrimSpace(doc.Find("title").First().Text())
	r.TitleLength = utf8.RuneCountInString(r.Title)
	r.TitleCount = doc.Find("title").Length()

	// Meta description
	r.MetaDescription = doc.Find(`meta[name="description"]`).AttrOr("content", "")
	r.DescriptionLength = utf8.RuneCountInString(r.MetaDescription)
	r.DescriptionCount = doc.Find(`meta[name="description"]`).Length()

	// Meta robots
	r.MetaRobots = doc.Find(`meta[name="robots"]`).AttrOr("content", "")

	// X-Robots-Tag
	r.XRobotsTag = responseHeaders.Get("X-Robots-Tag")

	// Indexability
	r.IndexabilityState = "indexable"
	if containsDirective(r.MetaRobots, "noindex") {
		r.IndexabilityState = "noindex_meta"
	} else if containsDirective(r.XRobotsTag, "noindex") {
		r.IndexabilityState = "noindex_header"
	}

	// Canonical
	extractCanonical(doc, baseURL, pageURL, r)

	// Rel next/prev
	doc.Find(`link[rel="next"]`).Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok {
			resolved := resolveURL(baseURL, href)
			r.RelNext = &RelLink{Raw: href, Resolved: resolved}
		}
	})
	doc.Find(`link[rel="prev"]`).Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok {
			resolved := resolveURL(baseURL, href)
			r.RelPrev = &RelLink{Raw: href, Resolved: resolved}
		}
	})

	// Hreflang
	doc.Find(`link[rel="alternate"][hreflang]`).Each(func(_ int, s *goquery.Selection) {
		lang, _ := s.Attr("hreflang")
		href, _ := s.Attr("href")
		if lang != "" && href != "" {
			r.Hreflangs = append(r.Hreflangs, HreflangEntry{
				Lang: lang,
				URL:  resolveURL(baseURL, href),
			})
		}
	})

	// Headings
	for level := 1; level <= 6; level++ {
		tag := fmt.Sprintf("h%d", level)
		doc.Find(tag).Each(func(_ int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text == "" {
				return
			}
			switch level {
			case 1:
				r.Headings.H1 = append(r.Headings.H1, text)
			case 2:
				r.Headings.H2 = append(r.Headings.H2, text)
			case 3:
				r.Headings.H3 = append(r.Headings.H3, text)
			case 4:
				r.Headings.H4 = append(r.Headings.H4, text)
			case 5:
				r.Headings.H5 = append(r.Headings.H5, text)
			case 6:
				r.Headings.H6 = append(r.Headings.H6, text)
			}
		})
	}

	// OG tags
	r.OpenGraph = OGTags{
		Title:       metaProperty(doc, "og:title"),
		Description: metaProperty(doc, "og:description"),
		Image:       metaProperty(doc, "og:image"),
		Type:        metaProperty(doc, "og:type"),
		URL:         metaProperty(doc, "og:url"),
	}

	// Twitter cards
	r.TwitterCard = TwitterTags{
		Card:        metaName(doc, "twitter:card"),
		Title:       metaName(doc, "twitter:title"),
		Description: metaName(doc, "twitter:description"),
		Image:       metaName(doc, "twitter:image"),
	}

	// JSON-LD
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		raw := strings.TrimSpace(s.Text())
		block := JSONLDBlock{Raw: raw}

		// Try as object first
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			// Try as array (e.g. [{"@type":"Organization"}, {"@type":"WebSite"}])
			var arr []interface{}
			if err := json.Unmarshal([]byte(raw), &arr); err != nil {
				block.Malformed = true
			} else {
				var types []string
				for _, item := range arr {
					if m, ok := item.(map[string]interface{}); ok {
						if t, ok := m["@type"]; ok {
							if ts, ok := t.(string); ok {
								types = append(types, ts)
							}
						}
					}
				}
				if len(types) > 0 {
					block.Type = strings.Join(types, ", ")
					r.JSONLDTypes = append(r.JSONLDTypes, types...)
				}
			}
		} else if t, ok := parsed["@type"]; ok {
			if ts, ok := t.(string); ok {
				block.Type = ts
				r.JSONLDTypes = append(r.JSONLDTypes, ts)
			}
		}
		r.JSONLDBlocks = append(r.JSONLDBlocks, block)
	})

	// Links
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		href = strings.TrimSpace(href)
		if shouldSkipHref(href) {
			return
		}
		resolved := resolveURL(baseURL, href)
		rel, _ := s.Attr("rel")
		target, _ := s.Attr("target")
		r.Links = append(r.Links, DiscoveredLink{
			URL:        resolved,
			AnchorText: strings.TrimSpace(s.Text()),
			Rel:        rel,
			Target:     target,
		})
		// Count protocol-relative URLs
		if strings.HasPrefix(href, "//") {
			r.ProtocolRelativeCount++
		}
	})

	// Images
	doc.Find("img").Each(func(_ int, s *goquery.Selection) {
		src, srcExists := s.Attr("src")
		if !srcExists || strings.TrimSpace(src) == "" {
			return // skip images without src
		}
		alt, altExists := s.Attr("alt")
		img := DiscoveredImage{
			Src:        resolveURL(baseURL, src),
			Alt:        alt,
			AltEmpty:   altExists && alt == "",
			AltMissing: !altExists,
			HasWidth:   s.AttrOr("width", "") != "",
			HasHeight:  s.AttrOr("height", "") != "",
		}
		r.Images = append(r.Images, img)
	})

	// Assets: scripts
	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		src = strings.TrimSpace(src)
		if src == "" || shouldSkipAssetURL(src) {
			return
		}
		r.Assets = append(r.Assets, DiscoveredAsset{
			URL:  resolveURL(baseURL, src),
			Type: "script",
		})
	})

	// Assets: link elements (stylesheets, preloads, icons)
	doc.Find("link").Each(func(_ int, s *goquery.Selection) {
		rel := strings.ToLower(strings.TrimSpace(s.AttrOr("rel", "")))
		href, hrefExists := s.Attr("href")
		if !hrefExists {
			return
		}
		href = strings.TrimSpace(href)
		if href == "" || shouldSkipAssetURL(href) {
			return
		}

		var assetType string
		switch {
		case rel == "stylesheet":
			assetType = "stylesheet"
		case rel == "icon" || rel == "shortcut icon" || rel == "apple-touch-icon":
			assetType = "icon"
		case rel == "preload":
			as := strings.ToLower(strings.TrimSpace(s.AttrOr("as", "")))
			switch as {
			case "font":
				assetType = "font"
			default:
				assetType = "preload"
			}
		case rel == "preconnect" || rel == "dns-prefetch":
			return // skip, no URL to check
		default:
			return // skip other link types (canonical, alternate, etc.)
		}

		r.Assets = append(r.Assets, DiscoveredAsset{
			URL:  resolveURL(baseURL, href),
			Type: assetType,
		})
	})

	// Assets: video
	doc.Find("video").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			src = strings.TrimSpace(src)
			if src != "" && !shouldSkipAssetURL(src) {
				r.Assets = append(r.Assets, DiscoveredAsset{
					URL:  resolveURL(baseURL, src),
					Type: "video",
				})
			}
		}
		s.Find("source[src]").Each(func(_ int, source *goquery.Selection) {
			src, _ := source.Attr("src")
			src = strings.TrimSpace(src)
			if src != "" && !shouldSkipAssetURL(src) {
				r.Assets = append(r.Assets, DiscoveredAsset{
					URL:  resolveURL(baseURL, src),
					Type: "video",
				})
			}
		})
	})

	// Assets: audio
	doc.Find("audio").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			src = strings.TrimSpace(src)
			if src != "" && !shouldSkipAssetURL(src) {
				r.Assets = append(r.Assets, DiscoveredAsset{
					URL:  resolveURL(baseURL, src),
					Type: "audio",
				})
			}
		}
		s.Find("source[src]").Each(func(_ int, source *goquery.Selection) {
			src, _ := source.Attr("src")
			src = strings.TrimSpace(src)
			if src != "" && !shouldSkipAssetURL(src) {
				r.Assets = append(r.Assets, DiscoveredAsset{
					URL:  resolveURL(baseURL, src),
					Type: "audio",
				})
			}
		})
	})

	// Script count + SPA root detection
	r.ScriptCount = doc.Find("script").Length()
	spaIDs := []string{"root", "__next", "app", "__nuxt"}
	for _, id := range spaIDs {
		if doc.Find("#" + id).Length() > 0 {
			r.HasSPARoot = true
			break
		}
	}

	// Word counts
	allText := ExtractVisibleText(doc)
	r.ExtractedWordCount = CountWords(allText)
	r.ExtractedText = allText
	r.ExtractedTextWithBounds = ExtractVisibleTextWithBoundaries(doc)

	mainText := ExtractMainContentText(doc)
	r.MainContentWordCount = CountWords(mainText)

	// Content hash
	hash := sha256.Sum256([]byte(allText))
	r.ContentHash = fmt.Sprintf("%x", hash)

	// JS suspect
	// Mark as JS suspect if: SPA root detected (always, since content depends on JS),
	// OR very little visible text with many scripts.
	r.JSSuspect = r.HasSPARoot || (r.ExtractedWordCount < 50 && r.ScriptCount >= 5)

	// Detect title outside <head> (inside <body>)
	doc.Find("body title").Each(func(_ int, s *goquery.Selection) {
		r.TitleOutsideHead = true
	})

	// Detect meta robots outside <head> (inside <body>)
	doc.Find("body meta[name='robots']").Each(func(_ int, s *goquery.Selection) {
		r.MetaRobotsOutsideHead = true
	})

	// Detect meta description outside <head> (inside <body>)
	doc.Find("body meta[name='description']").Each(func(_ int, s *goquery.Selection) {
		r.MetaDescriptionOutsideHead = true
	})

	// Canonical count and outside head
	r.CanonicalCount = doc.Find(`link[rel="canonical"]`).Length()
	doc.Find("body link[rel='canonical']").Each(func(_ int, s *goquery.Selection) {
		r.CanonicalOutsideHead = true
	})

	// First heading level (document order)
	r.FirstHeadingLevel = 0
	doc.Find("h1, h2, h3, h4, h5, h6").First().Each(func(_ int, s *goquery.Selection) {
		tagName := goquery.NodeName(s)
		if len(tagName) == 2 && tagName[0] == 'h' {
			r.FirstHeadingLevel = int(tagName[1] - '0')
		}
	})

	// H1 alt text only: H1 contains only an <img> with alt text, no visible text
	r.H1AltTextOnly = make([]string, 0)
	doc.Find("h1").Each(func(_ int, s *goquery.Selection) {
		// Clone and remove img elements to get text-only content
		clone := s.Clone()
		imgAlt := ""
		hasImg := false
		clone.Find("img").Each(func(_ int, img *goquery.Selection) {
			hasImg = true
			if alt, exists := img.Attr("alt"); exists && alt != "" {
				imgAlt = alt
			}
		})
		if !hasImg {
			return
		}
		// Get text content excluding img elements
		clone.Find("img").Remove()
		textOnly := strings.TrimSpace(clone.Text())
		if textOnly == "" && imgAlt != "" {
			r.H1AltTextOnly = append(r.H1AltTextOnly, imgAlt)
		}
	})

	// Protocol-relative URLs in images and assets
	doc.Find("img[src]").Each(func(_ int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok && strings.HasPrefix(strings.TrimSpace(src), "//") {
			r.ProtocolRelativeCount++
		}
	})
	doc.Find("script[src], link[href]").Each(func(_ int, s *goquery.Selection) {
		src := s.AttrOr("src", "")
		if src == "" {
			src = s.AttrOr("href", "")
		}
		if strings.HasPrefix(strings.TrimSpace(src), "//") {
			r.ProtocolRelativeCount++
		}
	})

	// Insecure form actions
	r.FormInsecureActions = make([]string, 0)
	doc.Find("form[action]").Each(func(_ int, s *goquery.Selection) {
		action, _ := s.Attr("action")
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(action)), "http://") {
			r.FormInsecureActions = append(r.FormInsecureActions, strings.TrimSpace(action))
		}
	})

	// Hreflang outside head
	doc.Find("body link[rel='alternate'][hreflang]").Each(func(_ int, s *goquery.Selection) {
		r.HreflangOutsideHead = true
	})

	// Invalid HTML in head
	r.InvalidHTMLInHead = make([]string, 0)
	invalidInHead := map[string]bool{}
	doc.Find("head div, head span, head p, head section, head article, head main, head footer, head header, head nav").Each(func(_ int, s *goquery.Selection) {
		tagName := goquery.NodeName(s)
		if !invalidInHead[tagName] {
			invalidInHead[tagName] = true
			r.InvalidHTMLInHead = append(r.InvalidHTMLInHead, tagName)
		}
	})

	// Head and body tag counts
	r.HeadTagCount = doc.Find("head").Length()
	r.BodyTagCount = doc.Find("body").Length()

	return r, nil
}

func extractCanonical(doc *goquery.Document, baseURL *url.URL, pageURL string, r *ParseResult) {
	canonical := doc.Find(`link[rel="canonical"]`).AttrOr("href", "")
	if canonical == "" {
		r.CanonicalType = "absent"
		return
	}

	r.CanonicalRaw = canonical
	r.CanonicalResolved = resolveURL(baseURL, canonical)

	if normalizeForComparison(r.CanonicalResolved) == normalizeForComparison(pageURL) {
		r.CanonicalType = "self"
	} else {
		r.CanonicalType = "cross"
	}
}

func resolveURL(base *url.URL, raw string) string {
	ref, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return base.ResolveReference(ref).String()
}

func normalizeForComparison(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	// Remove trailing slash for comparison.
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func containsDirective(val, directive string) bool {
	for _, part := range strings.Split(val, ",") {
		if strings.TrimSpace(strings.ToLower(part)) == directive {
			return true
		}
	}
	return false
}

func metaProperty(doc *goquery.Document, property string) string {
	return doc.Find(fmt.Sprintf(`meta[property="%s"]`, property)).AttrOr("content", "")
}

func metaName(doc *goquery.Document, name string) string {
	return doc.Find(fmt.Sprintf(`meta[name="%s"]`, name)).AttrOr("content", "")
}

var skipAssetPrefixes = []string{"data:", "blob:", "javascript:"}

func shouldSkipAssetURL(u string) bool {
	lower := strings.ToLower(strings.TrimSpace(u))
	for _, prefix := range skipAssetPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

var skipPrefixes = []string{"javascript:", "mailto:", "tel:", "data:", "blob:"}

func shouldSkipHref(href string) bool {
	lower := strings.ToLower(strings.TrimSpace(href))
	if lower == "" || lower == "#" {
		return true
	}
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
