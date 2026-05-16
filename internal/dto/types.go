// Package dto provides clean response types for MCP JSON serialization.
package dto

// PageDTO is the JSON-friendly representation of a crawled page.
type PageDTO struct {
	ID                    int64    `json:"id"`
	URL                   string   `json:"url"`
	Depth                 int      `json:"depth"`
	Title                 *string  `json:"title,omitempty"`
	TitleLength           *int     `json:"titleLength,omitempty"`
	MetaDescription       *string  `json:"metaDescription,omitempty"`
	MetaDescriptionLength *int     `json:"metaDescriptionLength,omitempty"`
	MetaRobots            *string  `json:"metaRobots,omitempty"`
	XRobotsTag            *string  `json:"xRobotsTag,omitempty"`
	IndexabilityState     string   `json:"indexabilityState"`
	CanonicalURL          *string  `json:"canonicalUrl,omitempty"`
	CanonicalIsSelf       *bool    `json:"canonicalIsSelf,omitempty"`
	CanonicalStatusCode   *int     `json:"canonicalStatusCode,omitempty"`
	RelNextURL            *string  `json:"relNextUrl,omitempty"`
	RelPrevURL            *string  `json:"relPrevUrl,omitempty"`
	HreflangJSON          *string  `json:"hreflangJson,omitempty"`
	H1JSON                *string  `json:"h1Json,omitempty"`
	H2JSON                *string  `json:"h2Json,omitempty"`
	H3JSON                *string  `json:"h3Json,omitempty"`
	H4JSON                *string  `json:"h4Json,omitempty"`
	H5JSON                *string  `json:"h5Json,omitempty"`
	H6JSON                *string  `json:"h6Json,omitempty"`
	OGTitle               *string  `json:"ogTitle,omitempty"`
	OGDescription         *string  `json:"ogDescription,omitempty"`
	OGImage               *string  `json:"ogImage,omitempty"`
	OGURL                 *string  `json:"ogUrl,omitempty"`
	OGType                *string  `json:"ogType,omitempty"`
	TwitterCard           *string  `json:"twitterCard,omitempty"`
	TwitterTitle          *string  `json:"twitterTitle,omitempty"`
	TwitterDescription    *string  `json:"twitterDescription,omitempty"`
	TwitterImage          *string  `json:"twitterImage,omitempty"`
	JSONLDRaw             *string  `json:"jsonldRaw,omitempty"`
	JSONLDTypesJSON       *string  `json:"jsonldTypesJson,omitempty"`
	ImagesJSON            *string  `json:"imagesJson,omitempty"`
	WordCount             *int     `json:"wordCount,omitempty"`
	MainContentWordCount  *int     `json:"mainContentWordCount,omitempty"`
	ContentHash           *string  `json:"contentHash,omitempty"`
	TextPreview           *string  `json:"textPreview,omitempty"`
	JSSuspect             bool     `json:"jsSuspect"`
	URLGroup              *string  `json:"urlGroup,omitempty"`
	OutboundEdgeCount     int      `json:"outboundEdgeCount"`
	InboundEdgeCount      int      `json:"inboundEdgeCount"`
	InboundLinkingPages   int      `json:"inboundLinkingPages"`
}

// EdgeDTO is the JSON-friendly representation of a link edge.
type EdgeDTO struct {
	ID                    int64   `json:"id"`
	SourceURL             string  `json:"sourceUrl"`
	TargetURL             *string `json:"targetUrl,omitempty"`
	SourceKind            string  `json:"sourceKind"`
	RelationType          string  `json:"relationType"`
	RelFlagsJSON          *string `json:"relFlagsJson,omitempty"`
	DiscoveryMode         string  `json:"discoveryMode"`
	AnchorText            *string `json:"anchorText,omitempty"`
	IsInternal            bool    `json:"isInternal"`
	DeclaredTargetURL     string  `json:"declaredTargetUrl"`
	FinalTargetURL        *string `json:"finalTargetUrl,omitempty"`
	TargetStatusCode      *int    `json:"targetStatusCode,omitempty"`
}

// IssueDTO is the JSON-friendly representation of an SEO issue.
type IssueDTO struct {
	ID          int64   `json:"id"`
	URL         *string `json:"url,omitempty"`
	IssueType   string  `json:"issueType"`
	Severity    string  `json:"severity"`
	Scope       string  `json:"scope"`
	DetailsJSON *string `json:"detailsJson,omitempty"`
}

// FetchDTO is the JSON-friendly representation of an HTTP fetch attempt.
type FetchDTO struct {
	ID                  int64   `json:"id"`
	FetchSeq            int     `json:"fetchSeq"`
	RequestedURL        string  `json:"requestedUrl"`
	FinalURL            *string `json:"finalUrl,omitempty"`
	StatusCode          *int    `json:"statusCode,omitempty"`
	RedirectHopCount    int     `json:"redirectHopCount"`
	TTFBMS              *int    `json:"ttfbMs,omitempty"`
	ResponseBodySize    *int    `json:"responseBodySize,omitempty"`
	ContentType         *string `json:"contentType,omitempty"`
	ContentEncoding     *string `json:"contentEncoding,omitempty"`
	ResponseHeadersJSON *string `json:"responseHeadersJson,omitempty"`
	HTTPMethod          string  `json:"httpMethod"`
	FetchKind           string  `json:"fetchKind"`
	RenderMode          string  `json:"renderMode"`
	RenderParamsJSON    *string `json:"renderParamsJson,omitempty"`
	FetchedAt           string  `json:"fetchedAt"`
	Error               *string `json:"error,omitempty"`
}

// CrawlSummaryDTO aggregates crawl-level statistics.
type CrawlSummaryDTO struct {
	JobID          string `json:"jobId"`
	Status         string `json:"status"`
	PagesCrawled   int    `json:"pagesCrawled"`
	URLsDiscovered int    `json:"urlsDiscovered"`
	IssuesFound    int    `json:"issuesFound"`
	StartedAt      *string `json:"startedAt,omitempty"`
	FinishedAt     *string `json:"finishedAt,omitempty"`
	Error          *string `json:"error,omitempty"`
}
