package issues

// IssueExplanation provides human-readable context for each issue type.
type IssueExplanation struct {
	Title       string // Human-readable title (e.g., "Invalid Structured Data")
	Description string // What it means (1-2 sentences)
	Impact      string // Why it matters for SEO (1 sentence)
	Fix         string // How to fix it (1 sentence)
}

// Explanations maps every known issue type string to its human-readable explanation.
var Explanations = map[string]IssueExplanation{
	// ── Page-local issues (from DetectPageLocalIssues) ──────────────────

	"missing_title": {
		Title:       "Missing Page Title",
		Description: "This page has no <title> tag in the HTML head.",
		Impact:      "Search engines use the title tag as the primary headline in search results. Without it, Google will auto-generate one, often poorly.",
		Fix:         "Add a unique, descriptive <title> tag between 30-60 characters.",
	},
	"title_too_short": {
		Title:       "Title Too Short",
		Description: "The page title is under 30 characters.",
		Impact:      "Short titles waste valuable SERP real estate and may not adequately describe the page content to users and search engines.",
		Fix:         "Expand the title to 30-60 characters with relevant keywords and a compelling description.",
	},
	"title_too_long": {
		Title:       "Title Too Long",
		Description: "The page title exceeds 60 characters.",
		Impact:      "Google typically truncates titles at ~60 characters in search results, meaning your full message won't be visible.",
		Fix:         "Trim the title to under 60 characters, front-loading the most important keywords.",
	},
	"missing_description": {
		Title:       "Missing Meta Description",
		Description: "This page has no meta description tag.",
		Impact:      "Without a meta description, search engines will auto-generate a snippet from page content, which may not be compelling or relevant.",
		Fix:         "Add a meta description between 70-160 characters that summarizes the page and includes a call to action.",
	},
	"description_too_short": {
		Title:       "Meta Description Too Short",
		Description: "The meta description is under 70 characters.",
		Impact:      "Short descriptions don't fully utilize the space available in search results, missing an opportunity to attract clicks.",
		Fix:         "Expand to 70-160 characters with a compelling summary and relevant keywords.",
	},
	"description_too_long": {
		Title:       "Meta Description Too Long",
		Description: "The meta description exceeds 160 characters.",
		Impact:      "Google truncates descriptions beyond ~160 characters, so the end of your message will be cut off in search results.",
		Fix:         "Trim to under 160 characters, keeping the most important information first.",
	},
	"missing_canonical": {
		Title:       "Missing Canonical Tag",
		Description: "This page does not declare a canonical URL.",
		Impact:      "Without a canonical tag, search engines may split ranking signals across duplicate or similar URLs, diluting page authority.",
		Fix:         "Add <link rel=\"canonical\" href=\"...\"> pointing to the preferred URL for this content.",
	},
	"missing_h1": {
		Title:       "Missing H1 Heading",
		Description: "This page has no <h1> tag.",
		Impact:      "The H1 is the primary heading that tells search engines and users what the page is about. Missing it weakens content signals.",
		Fix:         "Add a single, descriptive H1 heading that includes the page's primary keyword.",
	},
	"multiple_h1": {
		Title:       "Multiple H1 Headings",
		Description: "This page has more than one <h1> tag.",
		Impact:      "Multiple H1s dilute the primary topic signal and confuse the content hierarchy for search engines.",
		Fix:         "Keep a single H1 per page. Demote additional headings to H2 or H3.",
	},
	"thin_content": {
		Title:       "Thin Content",
		Description: "The page's main content has fewer words than the configured threshold (default: 200).",
		Impact:      "Pages with very little content are less likely to rank well, as search engines may consider them low-value.",
		Fix:         "Add substantive, relevant content. If the page is intentionally brief (e.g., a contact form), consider noindexing it.",
	},
	"missing_alt_attribute": {
		Title:       "Images Missing Alt Text",
		Description: "One or more images on this page have no alt attribute.",
		Impact:      "Alt text is critical for accessibility and helps search engines understand image content. Missing it hurts image SEO and accessibility compliance.",
		Fix:         "Add descriptive alt text to every image that conveys meaningful content.",
	},
	"empty_alt_attribute": {
		Title:       "Images With Empty Alt Text",
		Description: "One or more images have alt=\"\" (empty alt attribute).",
		Impact:      "Empty alt is valid for decorative images, but if these images convey content, they're invisible to screen readers and search engines.",
		Fix:         "If the image is decorative, empty alt is correct. If it conveys meaning, add descriptive alt text.",
	},
	"missing_og_title": {
		Title:       "Missing Open Graph Title",
		Description: "No og:title meta tag found.",
		Impact:      "Social media platforms use og:title for link previews. Without it, shares may show incorrect or generic titles.",
		Fix:         "Add <meta property=\"og:title\" content=\"...\"> matching or complementing the page title.",
	},
	"missing_og_description": {
		Title:       "Missing Open Graph Description",
		Description: "No og:description meta tag found.",
		Impact:      "Social shares will use auto-generated descriptions, which are often irrelevant or poorly formatted.",
		Fix:         "Add <meta property=\"og:description\" content=\"...\"> with a compelling social-specific description.",
	},
	"missing_og_image": {
		Title:       "Missing Open Graph Image",
		Description: "No og:image meta tag found.",
		Impact:      "Links shared on social media without an og:image appear as plain text links, dramatically reducing engagement.",
		Fix:         "Add <meta property=\"og:image\" content=\"...\"> with an image at least 1200x630px.",
	},
	"missing_structured_data": {
		Title:       "No Structured Data Found",
		Description: "This page has no JSON-LD structured data.",
		Impact:      "Structured data enables rich results in Google (stars, FAQs, breadcrumbs, etc.), giving your listing more visibility.",
		Fix:         "Add relevant JSON-LD markup (Organization, Article, Product, FAQ, etc.) based on the page type.",
	},
	"malformed_structured_data": {
		Title:       "Malformed Structured Data",
		Description: "A JSON-LD block on this page failed to parse as valid JSON.",
		Impact:      "Broken JSON-LD is completely ignored by search engines, as if it doesn't exist.",
		Fix:         "Validate the JSON-LD block with Google's Rich Results Test and fix syntax errors.",
	},
	"invalid_structured_data": {
		Title:       "Invalid Structured Data",
		Description: "A JSON-LD block is missing required properties for its declared @type.",
		Impact:      "Incomplete structured data may not qualify for rich results. Google requires specific properties per schema type.",
		Fix:         "Add the missing required properties. Check Google's structured data documentation for the specific @type.",
	},
	"incomplete_structured_data": {
		Title:       "Incomplete Structured Data",
		Description: "A JSON-LD block is missing recommended (but not required) properties for its @type.",
		Impact:      "While not blocking, adding recommended properties increases the chance of enhanced rich result features.",
		Fix:         "Add the missing recommended properties to maximize rich result eligibility.",
	},
	"mixed_content": {
		Title:       "Mixed Content (HTTP on HTTPS)",
		Description: "This HTTPS page references resources over HTTP.",
		Impact:      "Browsers may block mixed content, breaking functionality. It also signals a security concern to both users and search engines.",
		Fix:         "Update all resource URLs to use HTTPS.",
	},
	"robots_meta_header_mismatch": {
		Title:       "Robots Meta/Header Conflict",
		Description: "The meta robots tag and X-Robots-Tag HTTP header give conflicting directives.",
		Impact:      "Conflicting signals may cause unpredictable indexing behavior; search engines typically apply the most restrictive directive.",
		Fix:         "Align the meta robots tag and X-Robots-Tag header to use the same directives.",
	},
	"status_4xx": {
		Title:       "Client Error (4xx)",
		Description: "This URL returned a 4xx HTTP status code.",
		Impact:      "4xx pages waste crawl budget and create dead ends for users. If linked internally, they pass negative signals.",
		Fix:         "Fix or redirect the URL, and update or remove internal links pointing to it.",
	},
	"status_5xx": {
		Title:       "Server Error (5xx)",
		Description: "This URL returned a 5xx HTTP status code.",
		Impact:      "Server errors prevent indexing entirely. Persistent 5xx responses will cause Google to drop the page from search results.",
		Fix:         "Investigate and resolve the server-side error. Check application logs.",
	},
	"redirect_chain": {
		Title:       "Redirect Chain",
		Description: "This URL goes through multiple redirects before reaching the final destination.",
		Impact:      "Each redirect hop adds latency and may lose a small amount of link equity. Long chains also waste crawl budget.",
		Fix:         "Update links to point directly to the final destination URL, eliminating intermediate redirects.",
	},
	"redirect_loop": {
		Title:       "Redirect Loop",
		Description: "This URL is part of a circular redirect chain that never resolves.",
		Impact:      "The page is completely inaccessible; users get an error and search engines can't crawl it.",
		Fix:         "Break the redirect loop by updating one of the redirects to point to a valid, non-redirecting URL.",
	},
	"redirect_hops_exceeded": {
		Title:       "Too Many Redirect Hops",
		Description: "The redirect chain exceeded the maximum number of allowed hops (default: 10).",
		Impact:      "Browsers and search engine crawlers will give up following the chain, making the page unreachable.",
		Fix:         "Simplify the redirect chain to 1-2 hops maximum.",
	},
	"very_slow_response": {
		Title:       "Very Slow Response",
		Description: "The server's time to first byte (TTFB) exceeded 10 seconds for this page.",
		Impact:      "Extremely slow responses degrade user experience and are a strong negative signal for search rankings.",
		Fix:         "Investigate server performance, caching, database queries, and CDN configuration.",
	},
	"slow_response": {
		Title:       "Slow Response",
		Description: "The server's time to first byte (TTFB) exceeded 3 seconds for this page.",
		Impact:      "Slow server response degrades user experience and can negatively impact search rankings (Core Web Vitals).",
		Fix:         "Investigate server performance: check hosting, caching, database queries, and CDN configuration.",
	},
	"deep_page": {
		Title:       "Deep Page",
		Description: "This page is buried deep in the site hierarchy (many clicks from the homepage).",
		Impact:      "Deep pages get crawled less frequently and may be perceived as less important by search engines.",
		Fix:         "Reduce crawl depth by adding internal links from higher-level pages or improving site navigation.",
	},
	"title_outside_head": {
		Title:       "Title Outside <head>",
		Description: "The <title> element appears outside the <head> section, likely inside <body>.",
		Impact:      "Search engines may ignore the title if it's not in <head>. Google often still recognizes it, but this should not be relied upon.",
		Fix:         "Move the <title> element into the <head> section of the HTML document.",
	},
	"meta_robots_outside_head": {
		Title:       "Meta Robots Outside <head>",
		Description: "A <meta name=\"robots\"> tag appears outside the <head> section.",
		Impact:      "Search engines may ignore robots directives outside <head>. Critical directives like noindex could be missed.",
		Fix:         "Move the <meta name=\"robots\"> tag into the <head> section of the HTML document.",
	},
	"title_same_as_h1": {
		Title:       "Title Same as H1",
		Description: "The page title and the first H1 heading are identical.",
		Impact:      "Using the same text for both wastes an opportunity to target additional keywords and provide differentiated signals to search engines.",
		Fix:         "Differentiate the title and H1. The title should be optimized for SERPs while the H1 can be more descriptive for on-page readers.",
	},
	"multiple_title_tags": {
		Title:       "Multiple Title Tags",
		Description: "This page has more than one <title> element.",
		Impact:      "Browsers and search engines use the first <title> they encounter. Additional titles are ignored but signal messy HTML.",
		Fix:         "Remove duplicate <title> tags, keeping only one in the <head> section.",
	},
	"multiple_meta_descriptions": {
		Title:       "Multiple Meta Descriptions",
		Description: "This page has more than one <meta name=\"description\"> tag.",
		Impact:      "Search engines may pick an unpredictable description or ignore them all, leading to auto-generated snippets.",
		Fix:         "Keep a single meta description tag in the <head> section.",
	},
	"meta_description_outside_head": {
		Title:       "Meta Description Outside <head>",
		Description: "A <meta name=\"description\"> tag appears inside the <body> instead of the <head>.",
		Impact:      "Search engines may ignore meta descriptions outside <head>, resulting in auto-generated search snippets.",
		Fix:         "Move the <meta name=\"description\"> tag into the <head> section.",
	},
	"h1_too_long": {
		Title:       "H1 Too Long",
		Description: "An H1 heading exceeds 70 characters.",
		Impact:      "Overly long H1s dilute keyword focus and may be truncated in certain contexts.",
		Fix:         "Keep H1 headings concise and under 70 characters, focusing on the primary keyword.",
	},
	"h1_non_sequential": {
		Title:       "H1 Not First Heading",
		Description: "The first heading on the page is not an H1; a lower-level heading (H2-H6) appears first.",
		Impact:      "Non-sequential heading hierarchy confuses screen readers and weakens the content structure signal for search engines.",
		Fix:         "Ensure the first heading on the page is an H1, followed by H2s and deeper levels in order.",
	},
	"h1_alt_text_only": {
		Title:       "H1 Contains Only Image Alt Text",
		Description: "An H1 heading has no visible text; it only contains an image with alt text.",
		Impact:      "Search engines may not weight image-only H1s the same as text H1s, weakening the page's primary heading signal.",
		Fix:         "Add visible text to the H1 in addition to or instead of the image.",
	},
	"missing_h2": {
		Title:       "Missing H2 Headings",
		Description: "This page has no H2 headings.",
		Impact:      "H2 headings provide content structure and secondary keyword signals. Missing them suggests flat or poorly structured content.",
		Fix:         "Add H2 headings to break content into logical sections with relevant keywords.",
	},
	"h2_non_sequential": {
		Title:       "H2 Without Preceding H1",
		Description: "An H2 heading appears on the page without a preceding H1.",
		Impact:      "Skipping the H1 breaks the heading hierarchy, confusing assistive technologies and search engine content parsers.",
		Fix:         "Add an H1 heading before any H2 headings on the page.",
	},
	"h2_too_long": {
		Title:       "H2 Too Long",
		Description: "An H2 heading exceeds 70 characters.",
		Impact:      "Overly long H2s dilute keyword focus and may indicate content that should be restructured.",
		Fix:         "Keep H2 headings concise and under 70 characters.",
	},
	"multiple_canonicals": {
		Title:       "Multiple Canonical Tags",
		Description: "This page has more than one <link rel=\"canonical\"> tag.",
		Impact:      "Multiple canonicals send conflicting signals. Search engines may ignore them all and pick their own canonical.",
		Fix:         "Keep a single canonical tag in the <head> section pointing to the preferred URL.",
	},
	"canonical_is_relative": {
		Title:       "Relative Canonical URL",
		Description: "The canonical URL uses a relative path instead of an absolute URL.",
		Impact:      "While browsers resolve relative canonicals, Google recommends absolute URLs. Relative canonicals are more prone to errors.",
		Fix:         "Use a fully qualified absolute URL (starting with https://) in the canonical tag.",
	},
	"canonical_outside_head": {
		Title:       "Canonical Outside <head>",
		Description: "A <link rel=\"canonical\"> tag appears inside <body> instead of <head>.",
		Impact:      "Search engines may ignore canonical tags outside <head>, leading to incorrect canonical selection.",
		Fix:         "Move the <link rel=\"canonical\"> tag into the <head> section.",
	},
	"js_suspect_not_rendered": {
		Title:       "Suspected JavaScript-Rendered Content",
		Description: "This page appears to rely heavily on JavaScript for rendering its main content.",
		Impact:      "Search engines may not fully render JS content, meaning important text or links could be invisible to crawlers.",
		Fix:         "Implement server-side rendering (SSR) or pre-rendering for critical content.",
	},

	// ── Global issues (from DetectGlobalIssues) ─────────────────────────

	"duplicate_title": {
		Title:       "Duplicate Page Title",
		Description: "Multiple pages share the same <title> tag text.",
		Impact:      "Duplicate titles confuse search engines about which page to rank, and make search results look repetitive.",
		Fix:         "Give each page a unique, descriptive title that reflects its specific content.",
	},
	"duplicate_description": {
		Title:       "Duplicate Meta Description",
		Description: "Multiple pages share the same meta description.",
		Impact:      "Duplicate descriptions reduce click-through differentiation and signal low-quality content to search engines.",
		Fix:         "Write unique meta descriptions for each page that accurately summarize its content.",
	},
	"duplicate_content": {
		Title:       "Duplicate Content",
		Description: "Multiple pages have substantially identical body content.",
		Impact:      "Search engines may filter duplicate pages from results, wasting crawl budget and diluting ranking signals.",
		Fix:         "Consolidate duplicate pages with canonical tags, 301 redirects, or by differentiating the content.",
	},
	"orphan_page": {
		Title:       "Orphan Page",
		Description: "This page has no internal links pointing to it from other crawled pages.",
		Impact:      "Orphan pages are hard for search engines to discover and are treated as low-priority for crawling and indexing.",
		Fix:         "Add internal links from relevant pages, navigation menus, or sitemaps.",
	},
	"hreflang_not_reciprocal": {
		Title:       "Hreflang Not Reciprocal",
		Description: "This page declares an hreflang alternate, but the target page doesn't link back.",
		Impact:      "Non-reciprocal hreflang tags are ignored by Google, meaning your language/region targeting won't work.",
		Fix:         "Ensure both pages in the hreflang pair reference each other with matching hreflang tags.",
	},
	"broken_hreflang_target": {
		Title:       "Broken Hreflang Target",
		Description: "An hreflang alternate URL returns a non-200 status code.",
		Impact:      "Hreflang pointing to broken pages is ignored, and the language/region targeting fails entirely.",
		Fix:         "Fix the target URL to return 200, or update the hreflang to point to a valid page.",
	},
	"canonical_to_non_200": {
		Title:       "Canonical Points to Non-200",
		Description: "The canonical URL for this page returns a non-200 HTTP status.",
		Impact:      "A canonical pointing to a broken page sends confusing signals; search engines may ignore it and pick their own canonical.",
		Fix:         "Update the canonical tag to point to a valid, 200-status URL.",
	},
	"canonical_chain": {
		Title:       "Canonical Chain",
		Description: "The canonical URL itself has a different canonical, creating a chain.",
		Impact:      "Search engines may not follow canonical chains reliably, so the intended canonical may not be recognized.",
		Fix:         "Point the canonical directly to the final preferred URL, not through an intermediate page.",
	},
	"canonical_to_redirect": {
		Title:       "Canonical Points to Redirect",
		Description: "The canonical URL returns a 3xx redirect instead of a 200.",
		Impact:      "Canonicals should point to the final URL. Pointing to a redirect adds unnecessary indirection and may be ignored.",
		Fix:         "Update the canonical tag to point to the redirect's final destination URL.",
	},
	"broken_pagination_chain": {
		Title:       "Broken Pagination Chain",
		Description: "A rel=next/prev pagination link points to a non-200 page.",
		Impact:      "Broken pagination prevents search engines from discovering and indexing all pages in a paginated series.",
		Fix:         "Fix the target URL or update the pagination links to point to valid pages.",
	},
	"pagination_canonical_mismatch": {
		Title:       "Pagination Canonical Mismatch",
		Description: "A paginated page's canonical URL doesn't match its own URL.",
		Impact:      "If a paginated page canonicalizes to page 1, search engines may ignore pages 2+ entirely.",
		Fix:         "Each paginated page should have a self-referencing canonical, or use a view-all canonical if appropriate.",
	},
	"sitemap_non_200": {
		Title:       "Sitemap URL Returns Non-200",
		Description: "A URL listed in the sitemap returns a non-200 HTTP status.",
		Impact:      "Non-200 URLs in the sitemap waste crawl budget and signal poor site maintenance to search engines.",
		Fix:         "Remove non-200 URLs from the sitemap, or fix the underlying pages.",
	},
	"crawled_not_in_sitemap": {
		Title:       "Crawled Page Not in Sitemap",
		Description: "This indexable page was found by crawling but is not listed in the sitemap.",
		Impact:      "Pages missing from the sitemap may be crawled less frequently and could be deprioritized for indexing.",
		Fix:         "Add all important, indexable pages to the sitemap.",
	},
	"in_sitemap_not_crawled": {
		Title:       "Sitemap URL Not Crawled",
		Description: "A URL in the sitemap was not discovered or reached during the crawl.",
		Impact:      "This could indicate the page is not linked from anywhere on the site (orphan) or the crawl was too shallow.",
		Fix:         "Verify the URL is accessible and internally linked. Increase crawl depth if needed.",
	},
	"in_sitemap_robots_blocked": {
		Title:       "Sitemap URL Blocked by Robots",
		Description: "A URL listed in the sitemap is disallowed by robots.txt.",
		Impact:      "Contradictory signals: the sitemap says 'index this' but robots.txt says 'don't crawl.' Search engines will not index it.",
		Fix:         "Either remove the URL from the sitemap or remove the robots.txt disallow rule.",
	},
	"duplicate_h1": {
		Title:       "Duplicate H1 Heading",
		Description: "Multiple pages share the same H1 heading text.",
		Impact:      "Identical H1s across pages suggest thin or duplicated content and weaken the unique relevance signal for each page.",
		Fix:         "Give each page a unique H1 that reflects its specific content and target keywords.",
	},
	"duplicate_h2": {
		Title:       "Duplicate H2 Heading",
		Description: "Multiple pages share the same H2 heading text.",
		Impact:      "Repeated H2s across pages may indicate boilerplate content or poor content differentiation.",
		Fix:         "Ensure H2 headings are unique and relevant to each page's specific content.",
	},
	"non_indexable_canonical": {
		Title:       "Canonical Points to Non-Indexable Page",
		Description: "The canonical URL points to a page that has a noindex directive.",
		Impact:      "Canonicalizing to a noindex page sends contradictory signals: you're saying 'this is the preferred version' but that version says 'don't index me.'",
		Fix:         "Either remove the noindex from the canonical target or update the canonical to point to an indexable page.",
	},
	"unlinked_canonical": {
		Title:       "Unlinked Canonical URL",
		Description: "The canonical URL has no inbound internal links; it's only referenced via canonical declarations.",
		Impact:      "Pages discoverable only through canonicals may not be crawled efficiently. Internal links are the primary discovery mechanism.",
		Fix:         "Add internal links pointing to the canonical URL from relevant pages.",
	},
	"js_only_navigation": {
		Title:       "JS-Only Navigation Link",
		Description: "This internal link is only visible after JavaScript rendering, not in the static HTML source.",
		Impact:      "Search engines that don't execute JavaScript (or execute it poorly) won't discover this link. This reduces crawl efficiency and may prevent linked pages from being indexed.",
		Fix:         "Ensure navigation links use standard <a href> tags in the server-rendered HTML. For Next.js, verify the links are rendered server-side, not client-only.",
	},
	"http_to_https_missing": {
		Title:       "Missing HTTP to HTTPS Redirect",
		Description: "The site's HTTP version does not redirect to HTTPS.",
		Impact:      "Without an HTTP-to-HTTPS redirect, search engines may index the insecure version, splitting ranking signals.",
		Fix:         "Configure a 301 redirect from HTTP to HTTPS at the server level.",
	},

	// ── Batch B: Image issues ───────────────────────────────────────────

	"image_over_100kb": {
		Title:       "Image Over 100KB",
		Description: "This image file exceeds 100KB in size.",
		Impact:      "Large images slow page load times, hurt Core Web Vitals, and increase bandwidth costs for users on mobile or slow connections.",
		Fix:         "Compress the image, use modern formats (WebP, AVIF), and serve appropriately sized versions.",
	},
	"alt_text_too_long": {
		Title:       "Alt Text Too Long",
		Description: "One or more images have alt text exceeding 100 characters.",
		Impact:      "Excessively long alt text can be truncated by screen readers and may be seen as keyword stuffing by search engines.",
		Fix:         "Keep alt text concise and descriptive, ideally under 100 characters.",
	},
	"missing_image_size_attributes": {
		Title:       "Missing Image Size Attributes",
		Description: "One or more images lack explicit width and height attributes.",
		Impact:      "Without width/height attributes, the browser cannot reserve space for images before they load, causing layout shifts (poor CLS score).",
		Fix:         "Add width and height attributes to <img> tags to prevent Cumulative Layout Shift.",
	},

	// ── Batch B: Link issues ────────────────────────────────────────────

	"no_internal_outlinks": {
		Title:       "No Internal Outgoing Links",
		Description: "This page has zero internal outbound links.",
		Impact:      "Pages without internal links are dead ends for crawlers and users, failing to distribute link equity or guide navigation.",
		Fix:         "Add relevant internal links to help users and search engines discover related content.",
	},
	"non_descriptive_anchor_text": {
		Title:       "Non-Descriptive Anchor Text",
		Description: "Internal links use generic anchor text like 'click here' or 'read more'.",
		Impact:      "Descriptive anchor text helps search engines understand the linked page's topic. Generic anchors waste this ranking signal.",
		Fix:         "Replace generic anchor text with descriptive phrases that indicate the linked page's content.",
	},
	"internal_nofollow_outlink": {
		Title:       "Internal Link With Nofollow",
		Description: "An internal link uses rel=\"nofollow\", telling search engines not to follow it.",
		Impact:      "Nofollowing internal links wastes PageRank and prevents search engines from efficiently crawling your own site.",
		Fix:         "Remove rel=\"nofollow\" from internal links unless there's a specific reason (e.g., user-generated content).",
	},

	// ── Batch B: URL issues ─────────────────────────────────────────────

	"url_uppercase": {
		Title:       "URL Contains Uppercase Characters",
		Description: "The URL path contains uppercase letters.",
		Impact:      "URLs are case-sensitive on most servers. Uppercase URLs can cause duplicate content if both cases resolve to the same page.",
		Fix:         "Use lowercase URLs consistently and redirect uppercase variants to lowercase.",
	},
	"url_underscores": {
		Title:       "URL Contains Underscores",
		Description: "The URL path uses underscores instead of hyphens as word separators.",
		Impact:      "Google treats hyphens as word separators but not underscores. 'web_design' is one word to Google, 'web-design' is two.",
		Fix:         "Use hyphens (-) instead of underscores (_) in URLs.",
	},
	"url_contains_space": {
		Title:       "URL Contains Spaces",
		Description: "The URL contains encoded spaces (%20 or +).",
		Impact:      "Spaces in URLs look unprofessional, can cause encoding issues, and may break when shared or copied.",
		Fix:         "Replace spaces with hyphens in URLs.",
	},
	"url_has_parameters": {
		Title:       "URL Has Query Parameters",
		Description: "The URL includes query string parameters.",
		Impact:      "Parameterized URLs can cause duplicate content issues if not properly managed with canonical tags or parameter handling.",
		Fix:         "Use canonical tags, configure URL parameters in Search Console, or rewrite to clean URLs where appropriate.",
	},
	"url_too_long": {
		Title:       "URL Too Long",
		Description: "The URL exceeds 115 characters.",
		Impact:      "Long URLs are harder to share, may be truncated in search results, and can signal a deeply nested or poorly structured site.",
		Fix:         "Shorten URLs by removing unnecessary path segments, parameters, or verbose slugs.",
	},
	"url_multiple_slashes": {
		Title:       "URL Has Multiple Consecutive Slashes",
		Description: "The URL path contains consecutive forward slashes (e.g., //path//to//page).",
		Impact:      "Multiple slashes create duplicate URLs and can confuse search engines about the canonical version of the page.",
		Fix:         "Configure server-side redirects to normalize URLs with consecutive slashes to single slashes.",
	},
	"url_repetitive_path": {
		Title:       "URL Has Repetitive Path Segments",
		Description: "The URL contains repeating path segments (e.g., /blog/blog/post).",
		Impact:      "Repetitive path segments usually indicate a crawl trap, misconfigured routing, or URL generation bug that wastes crawl budget.",
		Fix:         "Fix the URL generation logic and redirect repetitive URLs to the correct canonical path.",
	},

	// ── Medium: Security Headers ───────────────────────────────────────

	"missing_hsts_header": {
		Title:       "Missing HSTS Header",
		Description: "This HTTPS page does not send a Strict-Transport-Security header.",
		Impact:      "Without HSTS, browsers may still allow HTTP connections, leaving users vulnerable to protocol downgrade attacks.",
		Fix:         "Add the Strict-Transport-Security header with an appropriate max-age directive (e.g., max-age=31536000).",
	},
	"missing_x_content_type_options": {
		Title:       "Missing X-Content-Type-Options Header",
		Description: "The response does not include X-Content-Type-Options: nosniff.",
		Impact:      "Without this header, browsers may MIME-sniff responses, potentially executing malicious content as a different type.",
		Fix:         "Add the header X-Content-Type-Options: nosniff to all responses.",
	},
	"missing_x_frame_options": {
		Title:       "Missing X-Frame-Options Header",
		Description: "The response does not include an X-Frame-Options header.",
		Impact:      "Without this header, the page can be embedded in iframes on other sites, making it vulnerable to clickjacking attacks.",
		Fix:         "Add X-Frame-Options: DENY or X-Frame-Options: SAMEORIGIN to prevent framing by external sites.",
	},
	"missing_content_security_policy": {
		Title:       "Missing Content Security Policy",
		Description: "The response does not include a Content-Security-Policy header.",
		Impact:      "Without CSP, the page has no protection against cross-site scripting (XSS) and data injection attacks.",
		Fix:         "Implement a Content-Security-Policy header that restricts resource loading to trusted sources.",
	},
	"missing_referrer_policy": {
		Title:       "Missing or Insecure Referrer Policy",
		Description: "The response has no Referrer-Policy header, or it uses an insecure value like unsafe-url.",
		Impact:      "Without a secure referrer policy, sensitive URL information may leak to external sites via the Referer header.",
		Fix:         "Add Referrer-Policy: strict-origin-when-cross-origin or a more restrictive policy.",
	},
	"unsafe_cross_origin_links": {
		Title:       "Unsafe Cross-Origin Links",
		Description: "External links with target=\"_blank\" are missing rel=\"noopener\" or rel=\"noreferrer\".",
		Impact:      "Without noopener/noreferrer, the linked page can access window.opener and potentially redirect your page or steal data.",
		Fix:         "Add rel=\"noopener noreferrer\" to all external links that use target=\"_blank\".",
	},
	"form_on_http": {
		Title:       "Insecure Form Submission",
		Description: "A form on this page submits data to an HTTP (non-HTTPS) URL.",
		Impact:      "Form data sent over HTTP is transmitted in plaintext, exposing sensitive user input to interception.",
		Fix:         "Change the form action to use HTTPS, or use a relative URL if the page is already on HTTPS.",
	},
	"protocol_relative_urls": {
		Title:       "Protocol-Relative URLs",
		Description: "Links or resources use protocol-relative URLs (starting with //) instead of explicit HTTPS.",
		Impact:      "Protocol-relative URLs inherit the page's protocol. On HTTP pages, this loads resources over insecure HTTP.",
		Fix:         "Replace protocol-relative URLs with explicit https:// URLs.",
	},

	// ── Medium: Hreflang ───────────────────────────────────────────────

	"hreflang_missing_self": {
		Title:       "Hreflang Missing Self-Reference",
		Description: "This page declares hreflang alternates but does not include itself in the list.",
		Impact:      "Google requires each page in an hreflang set to reference itself. Missing self-references can cause hreflang to be ignored.",
		Fix:         "Add a hreflang link pointing to the page's own URL with the appropriate language code.",
	},
	"hreflang_missing_x_default": {
		Title:       "Hreflang Missing x-default",
		Description: "This page has hreflang declarations but none with hreflang=\"x-default\".",
		Impact:      "Without x-default, search engines don't know which page to show users whose language doesn't match any declared alternate.",
		Fix:         "Add a hreflang=\"x-default\" link pointing to your default or language-selector page.",
	},
	"hreflang_invalid_language_code": {
		Title:       "Invalid Hreflang Language Code",
		Description: "An hreflang tag uses a language code that doesn't conform to ISO 639-1 or valid region codes.",
		Impact:      "Invalid language codes cause search engines to ignore the hreflang declaration entirely.",
		Fix:         "Use valid ISO 639-1 language codes (e.g., 'en', 'es', 'fr') optionally with ISO 3166-1 region codes (e.g., 'en-US').",
	},
	"hreflang_outside_head": {
		Title:       "Hreflang Link Outside Head",
		Description: "An hreflang <link> tag was found in the <body> instead of the <head>.",
		Impact:      "Search engines may not process hreflang declarations that appear outside the <head> section.",
		Fix:         "Move all <link rel=\"alternate\" hreflang=\"...\"> tags into the <head> section.",
	},

	// ── Medium: Sitemap Improvements ───────────────────────────────────

	"non_indexable_in_sitemap": {
		Title:       "Non-Indexable URL in Sitemap",
		Description: "A URL listed in the sitemap is not indexable (e.g., noindex, canonicalized away).",
		Impact:      "Including non-indexable URLs in sitemaps sends conflicting signals to search engines and wastes crawl budget.",
		Fix:         "Remove non-indexable URLs from the sitemap, or fix their indexability issues.",
	},
	"url_in_multiple_sitemaps": {
		Title:       "URL in Multiple Sitemaps",
		Description: "The same URL appears in more than one sitemap file.",
		Impact:      "Duplicate sitemap entries don't cause ranking harm but indicate disorganized sitemap management.",
		Fix:         "Deduplicate URLs across sitemap files to keep them clean and manageable.",
	},
	"sitemap_too_large": {
		Title:       "Sitemap Too Large",
		Description: "A sitemap file contains more than 50,000 URLs, exceeding the sitemap protocol limit.",
		Impact:      "Sitemaps exceeding 50K URLs or 50MB may not be fully processed by search engines.",
		Fix:         "Split large sitemaps into multiple files with fewer than 50,000 URLs each, and reference them from a sitemap index.",
	},

	// ── Medium: HTML Validation ────────────────────────────────────────

	"invalid_html_in_head": {
		Title:       "Invalid HTML Elements in Head",
		Description: "Non-standard elements (like <div>, <span>, <p>) were found inside the <head> section.",
		Impact:      "Invalid elements in <head> can cause browsers to prematurely close the head, pushing subsequent meta tags into the body where they won't be processed.",
		Fix:         "Remove or relocate non-meta elements from the <head> section. Only <title>, <meta>, <link>, <script>, <style>, and <base> belong in <head>.",
	},
	"multiple_head_tags": {
		Title:       "Multiple Head Tags",
		Description: "More than one <head> element was found in the HTML document.",
		Impact:      "Multiple <head> tags indicate malformed HTML that may cause browsers and search engines to misparse the document.",
		Fix:         "Ensure only one <head> element exists in the document. Check for template or CMS injection issues.",
	},
	"multiple_body_tags": {
		Title:       "Multiple Body Tags",
		Description: "More than one <body> element was found in the HTML document.",
		Impact:      "Multiple <body> tags indicate severely malformed HTML that may cause unpredictable rendering and indexing.",
		Fix:         "Ensure only one <body> element exists. Check for template concatenation or CMS issues.",
	},
	"html_too_large": {
		Title:       "HTML Document Too Large",
		Description: "The HTML response body exceeds 15MB.",
		Impact:      "Extremely large HTML documents slow down page load, waste bandwidth, and may not be fully indexed by search engines.",
		Fix:         "Reduce HTML size by lazy-loading content, paginating, or removing unnecessary inline data.",
	},

	// ── Medium: Content ────────────────────────────────────────────────

	"lorem_ipsum_detected": {
		Title:       "Lorem Ipsum Placeholder Text",
		Description: "The page contains 'lorem ipsum' placeholder text.",
		Impact:      "Placeholder text indicates unfinished content that provides no value to users or search engines.",
		Fix:         "Replace all lorem ipsum text with real, relevant content before publishing.",
	},
	"soft_404": {
		Title:       "Soft 404 Detected",
		Description: "This page returns HTTP 200 but its content strongly suggests it is an error page (e.g., 'page not found').",
		Impact:      "Soft 404s waste crawl budget because search engines index empty error pages that provide no value.",
		Fix:         "Return a proper 404 or 410 status code for pages that no longer exist, or add meaningful content.",
	},

	// ── Engine-level issues (from crawler engine) ───────────────────────

	"crawl_trap_suspected": {
		Title:       "Suspected Crawl Trap",
		Description: "This URL pattern generated an unusually high number of query string variants.",
		Impact:      "Crawl traps waste crawl budget on infinite URL variations that don't contain unique content.",
		Fix:         "Use rel=canonical, robots.txt, or URL parameter handling in Google Search Console to manage these URLs.",
	},
	"rate_limited": {
		Title:       "Rate Limited (429)",
		Description: "The server responded with HTTP 429 Too Many Requests.",
		Impact:      "Rate limiting indicates the server can't handle the crawl rate. Continued aggressive crawling may lead to IP blocking.",
		Fix:         "This is informational. The crawler automatically backs off. No action needed unless it affects most pages.",
	},
	"slow_host": {
		Title:       "Slow Server Response",
		Description: "Average TTFB for this host exceeds 5 seconds over the last 10 requests.",
		Impact:      "Slow server response degrades user experience and can negatively impact search rankings (Core Web Vitals).",
		Fix:         "Investigate server performance: check hosting, caching, database queries, and CDN configuration.",
	},
}
