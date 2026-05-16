package dto

import (
	"database/sql"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// URLLookup resolves a URL ID to its normalized URL string.
type URLLookup func(id int64) string

// PageFromStorage converts a storage.Page to a PageDTO.
func PageFromStorage(p storage.Page, lookup URLLookup) PageDTO {
	dto := PageDTO{
		ID:                  p.ID,
		URL:                 lookup(p.URLID),
		Depth:               int(p.Depth),
		IndexabilityState:   p.IndexabilityState,
		JSSuspect:           p.JSSuspect,
		OutboundEdgeCount:   int(p.OutboundEdgeCount),
		InboundEdgeCount:    int(p.InboundEdgeCount),
		InboundLinkingPages: int(p.InboundLinkingPages),
	}

	dto.Title = strPtr(p.Title)
	dto.StatusCode = intPtr(p.StatusCode)
	dto.ContentType = strPtr(p.ContentType)
	dto.TitleLength = intPtr(p.TitleLength)
	dto.MetaDescription = strPtr(p.MetaDescription)
	dto.MetaDescriptionLength = intPtr(p.MetaDescriptionLength)
	dto.MetaRobots = strPtr(p.MetaRobots)
	dto.XRobotsTag = strPtr(p.XRobotsTag)
	dto.CanonicalURL = strPtr(p.CanonicalURL)
	dto.CanonicalIsSelf = boolPtr(p.CanonicalIsSelf)
	dto.CanonicalStatusCode = intPtr(p.CanonicalStatusCode)
	dto.RelNextURL = strPtr(p.RelNextURL)
	dto.RelPrevURL = strPtr(p.RelPrevURL)
	dto.HreflangJSON = strPtr(p.HreflangJSON)
	dto.H1JSON = strPtr(p.H1JSON)
	dto.H2JSON = strPtr(p.H2JSON)
	dto.H3JSON = strPtr(p.H3JSON)
	dto.H4JSON = strPtr(p.H4JSON)
	dto.H5JSON = strPtr(p.H5JSON)
	dto.H6JSON = strPtr(p.H6JSON)
	dto.OGTitle = strPtr(p.OGTitle)
	dto.OGDescription = strPtr(p.OGDescription)
	dto.OGImage = strPtr(p.OGImage)
	dto.OGURL = strPtr(p.OGURL)
	dto.OGType = strPtr(p.OGType)
	dto.TwitterCard = strPtr(p.TwitterCard)
	dto.TwitterTitle = strPtr(p.TwitterTitle)
	dto.TwitterDescription = strPtr(p.TwitterDescription)
	dto.TwitterImage = strPtr(p.TwitterImage)
	dto.JSONLDRaw = strPtr(p.JSONLDRaw)
	dto.JSONLDTypesJSON = strPtr(p.JSONLDTypesJSON)
	dto.ImagesJSON = strPtr(p.ImagesJSON)
	dto.WordCount = intPtr(p.WordCount)
	dto.MainContentWordCount = intPtr(p.MainContentWordCount)
	dto.ContentHash = strPtr(p.ContentHash)
	dto.TextPreview = strPtr(p.TextPreview)
	dto.URLGroup = strPtr(p.URLGroup)

	return dto
}

// EdgeFromStorage converts a storage.Edge to an EdgeDTO.
func EdgeFromStorage(e storage.Edge, lookup URLLookup) EdgeDTO {
	dto := EdgeDTO{
		ID:                e.ID,
		SourceURL:         lookup(e.SourceURLID),
		SourceKind:        e.SourceKind,
		RelationType:      e.RelationType,
		RelFlagsJSON:      strPtr(e.RelFlagsJSON),
		DiscoveryMode:     e.DiscoveryMode,
		AnchorText:        strPtr(e.AnchorText),
		IsInternal:        e.IsInternal,
		DeclaredTargetURL: e.DeclaredTargetURL,
	}

	if e.NormalizedTargetURLID.Valid {
		u := lookup(e.NormalizedTargetURLID.Int64)
		dto.TargetURL = &u
	}
	if e.FinalTargetURLID.Valid {
		u := lookup(e.FinalTargetURLID.Int64)
		dto.FinalTargetURL = &u
	}
	dto.TargetStatusCode = intPtr(e.TargetStatusCode)

	return dto
}

// IssueFromStorage converts a storage.Issue to an IssueDTO.
func IssueFromStorage(i storage.Issue, lookup URLLookup) IssueDTO {
	dto := IssueDTO{
		ID:          i.ID,
		IssueType:   i.IssueType,
		Severity:    i.Severity,
		Scope:       i.Scope,
		DetailsJSON: strPtr(i.DetailsJSON),
	}

	if i.URLID.Valid {
		u := lookup(i.URLID.Int64)
		dto.URL = &u
	}

	return dto
}

// FetchFromStorage converts a storage.Fetch to a FetchDTO.
func FetchFromStorage(f storage.Fetch, lookup URLLookup) FetchDTO {
	dto := FetchDTO{
		ID:               f.ID,
		FetchSeq:         int(f.FetchSeq),
		RequestedURL:     lookup(f.RequestedURLID),
		RedirectHopCount: int(f.RedirectHopCount),
		HTTPMethod:       f.HTTPMethod,
		FetchKind:        f.FetchKind,
		RenderMode:       f.RenderMode,
		FetchedAt:        f.FetchedAt,
	}

	if f.FinalURLID.Valid {
		u := lookup(f.FinalURLID.Int64)
		dto.FinalURL = &u
	}
	dto.StatusCode = intPtr(f.StatusCode)
	dto.TTFBMS = intPtr(f.TTFBMS)
	dto.ResponseBodySize = intPtr(f.ResponseBodySize)
	dto.ContentType = strPtr(f.ContentType)
	dto.ContentEncoding = strPtr(f.ContentEncoding)
	dto.ResponseHeadersJSON = strPtr(f.ResponseHeadersJSON)
	dto.RenderParamsJSON = strPtr(f.RenderParamsJSON)
	dto.Error = strPtr(f.Error)

	return dto
}

// CrawlSummaryFromStorage converts a storage.CrawlJob to a CrawlSummaryDTO.
func CrawlSummaryFromStorage(j storage.CrawlJob) CrawlSummaryDTO {
	return CrawlSummaryDTO{
		JobID:          j.ID,
		Status:         j.Status,
		PagesCrawled:   j.PagesCrawled,
		URLsDiscovered: j.URLsDiscovered,
		IssuesFound:    j.IssuesFound,
		StartedAt:      strPtr(j.StartedAt),
		FinishedAt:     strPtr(j.FinishedAt),
		Error:          strPtr(j.Error),
	}
}

// --- helpers ---

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func intPtr(ni sql.NullInt64) *int {
	if !ni.Valid {
		return nil
	}
	v := int(ni.Int64)
	return &v
}

func boolPtr(ni sql.NullInt64) *bool {
	if !ni.Valid {
		return nil
	}
	v := ni.Int64 != 0
	return &v
}
