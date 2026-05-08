// Package storage provides SQLite persistence for crawl data.
package storage

import "database/sql"

// CrawlJob represents a top-level crawl session.
type CrawlJob struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Status         string         `json:"status"`
	ConfigJSON     string         `json:"configJson"`
	SeedURLs       string         `json:"seedUrls"`
	CreatedAt      string         `json:"createdAt"`
	StartedAt      sql.NullString `json:"startedAt,omitempty"`
	FinishedAt     sql.NullString `json:"finishedAt,omitempty"`
	Error          sql.NullString `json:"error,omitempty"`
	PagesCrawled   int            `json:"pagesCrawled"`
	URLsDiscovered int            `json:"urlsDiscovered"`
	IssuesFound    int            `json:"issuesFound"`
	TTLExpiresAt   sql.NullString `json:"ttlExpiresAt,omitempty"`
}

// URL represents a discovered URL within a crawl.
type URL struct {
	ID            int64  `json:"id"`
	JobID         string `json:"jobId"`
	NormalizedURL string `json:"normalizedUrl"`
	Host          string `json:"host"`
	Status        string `json:"status"`
	IsInternal    bool   `json:"isInternal"`
	DiscoveredVia string `json:"discoveredVia"`
	CreatedAt     string `json:"createdAt"`
}

// Fetch represents an HTTP fetch attempt.
type Fetch struct {
	ID                  int64          `json:"id"`
	JobID               string         `json:"jobId"`
	FetchSeq            int64          `json:"fetchSeq"`
	RequestedURLID      int64          `json:"requestedUrlId"`
	FinalURLID          sql.NullInt64  `json:"finalUrlId,omitempty"`
	StatusCode          sql.NullInt64  `json:"statusCode,omitempty"`
	RedirectHopCount    int64          `json:"redirectHopCount"`
	TTFBMS              sql.NullInt64  `json:"ttfbMs,omitempty"`
	ResponseBodySize    sql.NullInt64  `json:"responseBodySize,omitempty"`
	ContentType         sql.NullString `json:"contentType,omitempty"`
	ContentEncoding     sql.NullString `json:"contentEncoding,omitempty"`
	ResponseHeadersJSON sql.NullString `json:"responseHeadersJson,omitempty"`
	HTTPMethod          string         `json:"httpMethod"`
	FetchKind           string         `json:"fetchKind"`
	RenderMode          string         `json:"renderMode"`
	RenderParamsJSON    sql.NullString `json:"renderParamsJson,omitempty"`
	FetchedAt           string         `json:"fetchedAt"`
	Error               sql.NullString `json:"error,omitempty"`
}

// Page holds parsed SEO data for a single page.
type Page struct {
	ID                      int64          `json:"id"`
	JobID                   string         `json:"jobId"`
	URLID                   int64          `json:"urlId"`
	FetchID                 int64          `json:"fetchId"`
	Depth                   int64          `json:"depth"`
	Title                   sql.NullString `json:"title,omitempty"`
	TitleLength             sql.NullInt64  `json:"titleLength,omitempty"`
	MetaDescription         sql.NullString `json:"metaDescription,omitempty"`
	MetaDescriptionLength   sql.NullInt64  `json:"metaDescriptionLength,omitempty"`
	MetaRobots              sql.NullString `json:"metaRobots,omitempty"`
	XRobotsTag              sql.NullString `json:"xRobotsTag,omitempty"`
	IndexabilityState       string         `json:"indexabilityState"`
	CanonicalURL            sql.NullString `json:"canonicalUrl,omitempty"`
	CanonicalIsSelf         sql.NullInt64  `json:"canonicalIsSelf,omitempty"`
	CanonicalStatusCode     sql.NullInt64  `json:"canonicalStatusCode,omitempty"`
	RelNextURL              sql.NullString `json:"relNextUrl,omitempty"`
	RelPrevURL              sql.NullString `json:"relPrevUrl,omitempty"`
	HreflangJSON            sql.NullString `json:"hreflangJson,omitempty"`
	H1JSON                  sql.NullString `json:"h1Json,omitempty"`
	H2JSON                  sql.NullString `json:"h2Json,omitempty"`
	H3JSON                  sql.NullString `json:"h3Json,omitempty"`
	H4JSON                  sql.NullString `json:"h4Json,omitempty"`
	H5JSON                  sql.NullString `json:"h5Json,omitempty"`
	H6JSON                  sql.NullString `json:"h6Json,omitempty"`
	OGTitle                 sql.NullString `json:"ogTitle,omitempty"`
	OGDescription           sql.NullString `json:"ogDescription,omitempty"`
	OGImage                 sql.NullString `json:"ogImage,omitempty"`
	OGURL                   sql.NullString `json:"ogUrl,omitempty"`
	OGType                  sql.NullString `json:"ogType,omitempty"`
	TwitterCard             sql.NullString `json:"twitterCard,omitempty"`
	TwitterTitle            sql.NullString `json:"twitterTitle,omitempty"`
	TwitterDescription      sql.NullString `json:"twitterDescription,omitempty"`
	TwitterImage            sql.NullString `json:"twitterImage,omitempty"`
	JSONLDRaw               sql.NullString `json:"jsonldRaw,omitempty"`
	JSONLDTypesJSON         sql.NullString `json:"jsonldTypesJson,omitempty"`
	ImagesJSON              sql.NullString `json:"imagesJson,omitempty"`
	WordCount               sql.NullInt64  `json:"wordCount,omitempty"`
	MainContentWordCount    sql.NullInt64  `json:"mainContentWordCount,omitempty"`
	ContentHash             sql.NullString `json:"contentHash,omitempty"`
	JSSuspect               bool           `json:"jsSuspect"`
	URLGroup                sql.NullString `json:"urlGroup,omitempty"`
	OutboundEdgeCount       int64          `json:"outboundEdgeCount"`
	InboundEdgeCount        int64          `json:"inboundEdgeCount"`
	InboundLinkingPages     int64          `json:"inboundLinkingPages"`
}

// Edge represents a link between two URLs.
type Edge struct {
	ID                    int64          `json:"id"`
	JobID                 string         `json:"jobId"`
	SourceURLID           int64          `json:"sourceUrlId"`
	NormalizedTargetURLID sql.NullInt64  `json:"normalizedTargetUrlId,omitempty"`
	SourceKind            string         `json:"sourceKind"`
	RelationType          string         `json:"relationType"`
	RelFlagsJSON          sql.NullString `json:"relFlagsJson,omitempty"`
	DiscoveryMode         string         `json:"discoveryMode"`
	AnchorText            sql.NullString `json:"anchorText,omitempty"`
	IsInternal            bool           `json:"isInternal"`
	DeclaredTargetURL     string         `json:"declaredTargetUrl"`
	FinalTargetURLID      sql.NullInt64  `json:"finalTargetUrlId,omitempty"`
	TargetStatusCode      sql.NullInt64  `json:"targetStatusCode,omitempty"`
}

// RedirectHop represents a single hop in a redirect chain.
type RedirectHop struct {
	ID         int64  `json:"id"`
	JobID      string `json:"jobId"`
	FetchID    int64  `json:"fetchId"`
	HopIndex   int64  `json:"hopIndex"`
	StatusCode int64  `json:"statusCode"`
	FromURL    string `json:"fromUrl"`
	ToURL      string `json:"toUrl"`
}

// SitemapEntry represents a URL found in a sitemap.
type SitemapEntry struct {
	ID                    int64            `json:"id"`
	JobID                 string           `json:"jobId"`
	URL                   string           `json:"url"`
	SourceSitemapURL      string           `json:"sourceSitemapUrl"`
	SourceHost            string           `json:"sourceHost"`
	Lastmod               sql.NullString   `json:"lastmod,omitempty"`
	Changefreq            sql.NullString   `json:"changefreq,omitempty"`
	Priority              sql.NullFloat64  `json:"priority,omitempty"`
	ReconciliationStatus  string           `json:"reconciliationStatus"`
}

// RobotsDirective represents a parsed robots.txt rule.
type RobotsDirective struct {
	ID          int64  `json:"id"`
	JobID       string `json:"jobId"`
	Host        string `json:"host"`
	UserAgent   string `json:"userAgent"`
	RuleType    string `json:"ruleType"`
	PathPattern string `json:"pathPattern"`
	SourceURL   string `json:"sourceUrl"`
}

// LlmsFinding holds llms.txt analysis results for a host.
type LlmsFinding struct {
	ID                int64          `json:"id"`
	JobID             string         `json:"jobId"`
	Host              string         `json:"host"`
	Present           bool           `json:"present"`
	RawContent        sql.NullString `json:"rawContent,omitempty"`
	SectionsJSON      sql.NullString `json:"sectionsJson,omitempty"`
	ReferencedURLsJSON sql.NullString `json:"referencedUrlsJson,omitempty"`
}

// Asset represents an external resource (CSS, JS, image, font).
type Asset struct {
	ID            int64          `json:"id"`
	JobID         string         `json:"jobId"`
	URLID         int64          `json:"urlId"`
	ContentType   sql.NullString `json:"contentType,omitempty"`
	StatusCode    sql.NullInt64  `json:"statusCode,omitempty"`
	ContentLength sql.NullInt64  `json:"contentLength,omitempty"`
}

// AssetReference links a page to an asset it references.
type AssetReference struct {
	ID               int64  `json:"id"`
	JobID            string `json:"jobId"`
	AssetURLID       int64  `json:"assetUrlId"`
	SourcePageURLID  int64  `json:"sourcePageUrlId"`
	ReferenceType    string `json:"referenceType"`
}

// Issue represents a detected SEO issue.
type Issue struct {
	ID          int64          `json:"id"`
	JobID       string         `json:"jobId"`
	URLID       sql.NullInt64  `json:"urlId,omitempty"`
	IssueType   string         `json:"issueType"`
	Severity    string         `json:"severity"`
	Scope       string         `json:"scope"`
	DetailsJSON sql.NullString `json:"detailsJson,omitempty"`
}

// CrawlEvent represents a timestamped crawl activity event.
type CrawlEvent struct {
	ID          int64          `json:"id"`
	JobID       string         `json:"jobId"`
	Timestamp   string         `json:"timestamp"`
	EventType   string         `json:"eventType"`
	DetailsJSON sql.NullString `json:"detailsJson,omitempty"`
	URL         sql.NullString `json:"url,omitempty"`
}

// URLPatternGroup defines a URL grouping pattern.
type URLPatternGroup struct {
	ID      int64  `json:"id"`
	JobID   string `json:"jobId"`
	Pattern string `json:"pattern"`
	Name    string `json:"name"`
	Source  string `json:"source"`
}

// CanonicalCluster represents a group of URLs pointing to the same canonical.
type CanonicalCluster struct {
	ID                int64         `json:"id"`
	JobID             string        `json:"jobId"`
	ClusterURL        string        `json:"clusterUrl"`
	MemberCount       int           `json:"memberCount"`
	TargetStatusCode  sql.NullInt64 `json:"targetStatusCode,omitempty"`
	IsSelfReferencing bool          `json:"isSelfReferencing"`
}

// CanonicalClusterMember links a URL to its canonical cluster.
type CanonicalClusterMember struct {
	ClusterID int64 `json:"clusterId"`
	URLID     int64 `json:"urlId"`
}

// DuplicateCluster represents a group of URLs with identical content.
type DuplicateCluster struct {
	ID          int64  `json:"id"`
	JobID       string `json:"jobId"`
	ClusterType string `json:"clusterType"`
	HashValue   string `json:"hashValue"`
	FirstURLID  int64  `json:"firstUrlId"`
	MemberCount int    `json:"memberCount"`
}

// DuplicateClusterMember links a URL to its duplicate cluster.
type DuplicateClusterMember struct {
	ClusterID int64 `json:"clusterId"`
	URLID     int64 `json:"urlId"`
	FetchSeq  int   `json:"fetchSeq"`
}
