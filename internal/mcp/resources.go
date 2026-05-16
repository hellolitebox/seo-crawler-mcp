package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// registerResources adds all resource and resource template handlers.
func (s *Server) registerResources() {
	// Static resource: job list
	s.mcpServer.AddResource(gomcp.NewResource(
		"seo-crawler://jobs",
		"List of all crawl jobs",
		gomcp.WithMIMEType("application/json"),
	), s.handleJobListResource)

	// Dynamic resource templates
	s.mcpServer.AddResourceTemplate(gomcp.NewResourceTemplate(
		"seo-crawler://jobs/{jobId}",
		"Crawl job details",
		gomcp.WithTemplateMIMEType("application/json"),
	), s.handleJobDetailResource)

	s.mcpServer.AddResourceTemplate(gomcp.NewResourceTemplate(
		"seo-crawler://jobs/{jobId}/summary",
		"Crawl summary snapshot",
		gomcp.WithTemplateMIMEType("application/json"),
	), s.handleJobSummaryResource)

	s.mcpServer.AddResourceTemplate(gomcp.NewResourceTemplate(
		"seo-crawler://jobs/{jobId}/events",
		"Crawl event log",
		gomcp.WithTemplateMIMEType("application/json"),
	), s.handleJobEventsResource)

	s.mcpServer.AddResourceTemplate(gomcp.NewResourceTemplate(
		"seo-crawler://jobs/{jobId}/page/{urlId}",
		"Single page detail with edges, issues, and redirect hops",
		gomcp.WithTemplateMIMEType("application/json"),
	), s.handlePageDetailResource)
}

// handleJobListResource returns all crawl jobs as JSON.
func (s *Server) handleJobListResource(
	ctx context.Context,
	req gomcp.ReadResourceRequest,
) ([]gomcp.ResourceContents, error) {
	if s.db == nil {
		return nil, fmt.Errorf("server not configured: database unavailable")
	}
	jobs, err := s.db.ListJobs()
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}

	data, err := json.Marshal(jobs)
	if err != nil {
		return nil, fmt.Errorf("marshalling jobs: %w", err)
	}

	return []gomcp.ResourceContents{
		gomcp.TextResourceContents{
			URI:      "seo-crawler://jobs",
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// extractJobID extracts the job ID from a resource URI.
// Expected formats: seo-crawler://jobs/{jobId}[/suffix]
func extractJobID(uri string) (string, error) {
	const prefix = "seo-crawler://jobs/"
	if !strings.HasPrefix(uri, prefix) {
		return "", fmt.Errorf("invalid resource URI %q", uri)
	}
	rest := strings.TrimPrefix(uri, prefix)
	// Take everything up to the next slash (or end)
	parts := strings.SplitN(rest, "/", 2)
	if parts[0] == "" {
		return "", fmt.Errorf("missing job ID in URI %q", uri)
	}
	return parts[0], nil
}

// jobDetailPayload is the JSON structure for the job detail resource.
type jobDetailPayload struct {
	Job          any            `json:"job"`
	URLsByStatus map[string]int `json:"urlsByStatus"`
	IssuesByType map[string]int `json:"issuesByType"`
}

// handleJobDetailResource returns full job detail with counters.
func (s *Server) handleJobDetailResource(
	ctx context.Context,
	req gomcp.ReadResourceRequest,
) ([]gomcp.ResourceContents, error) {
	if s.db == nil {
		return nil, fmt.Errorf("server not configured: database unavailable")
	}
	jobID, err := extractJobID(req.Params.URI)
	if err != nil {
		return nil, err
	}

	job, err := s.db.GetJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("getting job %q: %w", jobID, err)
	}

	urlsByStatus, err := s.db.CountURLsByStatus(jobID)
	if err != nil {
		return nil, fmt.Errorf("counting URLs for job %q: %w", jobID, err)
	}

	issuesByType, err := s.db.CountIssuesByType(jobID)
	if err != nil {
		return nil, fmt.Errorf("counting issues for job %q: %w", jobID, err)
	}

	payload := jobDetailPayload{
		Job:          job,
		URLsByStatus: urlsByStatus,
		IssuesByType: issuesByType,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling job detail: %w", err)
	}

	return []gomcp.ResourceContents{
		gomcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// handleJobSummaryResource returns the crawl summary snapshot.
func (s *Server) handleJobSummaryResource(
	ctx context.Context,
	req gomcp.ReadResourceRequest,
) ([]gomcp.ResourceContents, error) {
	if s.db == nil {
		return nil, fmt.Errorf("server not configured: database unavailable")
	}
	jobID, err := extractJobID(req.Params.URI)
	if err != nil {
		return nil, err
	}

	summary, err := s.db.GetCrawlSummary(jobID)
	if err != nil {
		return nil, fmt.Errorf("getting summary for job %q: %w", jobID, err)
	}

	data, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("marshalling summary: %w", err)
	}

	return []gomcp.ResourceContents{
		gomcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// handleJobEventsResource returns the crawl event log.
func (s *Server) handleJobEventsResource(
	ctx context.Context,
	req gomcp.ReadResourceRequest,
) ([]gomcp.ResourceContents, error) {
	if s.db == nil {
		return nil, fmt.Errorf("server not configured: database unavailable")
	}
	jobID, err := extractJobID(req.Params.URI)
	if err != nil {
		return nil, err
	}

	events, err := s.db.GetEventsByJob(jobID, 1000)
	if err != nil {
		return nil, fmt.Errorf("getting events for job %q: %w", jobID, err)
	}

	data, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("marshalling events: %w", err)
	}

	return []gomcp.ResourceContents{
		gomcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// extractURLID extracts the job ID and URL ID from a page detail resource URI.
// Expected format: seo-crawler://jobs/{jobId}/page/{urlId}
func extractURLID(uri string) (string, int64, error) {
	const prefix = "seo-crawler://jobs/"
	if !strings.HasPrefix(uri, prefix) {
		return "", 0, fmt.Errorf("invalid resource URI %q", uri)
	}
	rest := strings.TrimPrefix(uri, prefix)
	// Expected: {jobId}/page/{urlId}
	parts := strings.SplitN(rest, "/page/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, fmt.Errorf("invalid page detail URI %q: expected seo-crawler://jobs/{jobId}/page/{urlId}", uri)
	}
	urlID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid URL ID %q in URI %q: %w", parts[1], uri, err)
	}
	return parts[0], urlID, nil
}

// pageDetailPayload is the JSON structure for the page detail resource.
type pageDetailPayload struct {
	URL           *storage.URL          `json:"url"`
	Page          *storage.Page         `json:"page,omitempty"`
	Fetch         *storage.Fetch        `json:"fetch,omitempty"`
	Issues        []storage.Issue       `json:"issues"`
	OutboundEdges []storage.Edge        `json:"outboundEdges"`
	InboundEdges  []storage.Edge        `json:"inboundEdges"`
	RedirectHops  []storage.RedirectHop `json:"redirectHops"`
}

// handlePageDetailResource returns a single page detail with edges, issues, and redirect hops.
func (s *Server) handlePageDetailResource(
	ctx context.Context,
	req gomcp.ReadResourceRequest,
) ([]gomcp.ResourceContents, error) {
	if s.db == nil {
		return nil, fmt.Errorf("server not configured: database unavailable")
	}
	jobID, urlID, err := extractURLID(req.Params.URI)
	if err != nil {
		return nil, err
	}

	// Get the URL record
	urlRecord, err := s.db.GetURL(urlID)
	if err != nil {
		return nil, fmt.Errorf("getting URL %d: %w", urlID, err)
	}
	if urlRecord.JobID != jobID {
		return nil, fmt.Errorf("URL %d does not belong to job %q", urlID, jobID)
	}

	payload := pageDetailPayload{
		URL:           urlRecord,
		Issues:        []storage.Issue{},
		OutboundEdges: []storage.Edge{},
		InboundEdges:  []storage.Edge{},
		RedirectHops:  []storage.RedirectHop{},
	}

	// Get page data (may not exist for non-crawled URLs)
	page, err := s.db.GetPageByURL(jobID, urlID)
	if err == nil {
		payload.Page = page
	}

	// Get fetch data
	fetch, err := s.db.GetFetchByURL(jobID, urlID)
	if err == nil {
		payload.Fetch = fetch
		// Get redirect hops from the fetch
		hops, hopErr := s.db.GetRedirectHopsByFetch(fetch.ID)
		if hopErr == nil {
			payload.RedirectHops = hops
		}
	}

	// Get issues for this URL
	issues, err := s.db.GetIssuesByURL(jobID, urlID)
	if err == nil {
		payload.Issues = issues
	}

	// Get outbound edges (limit 200)
	outbound, err := s.db.GetEdgesBySource(jobID, urlID, 200, "")
	if err == nil {
		payload.OutboundEdges = outbound
	}

	// Get inbound edges (limit 200)
	inbound, err := s.db.GetEdgesByTarget(jobID, urlID, 200, "")
	if err == nil {
		payload.InboundEdges = inbound
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling page detail: %w", err)
	}

	return []gomcp.ResourceContents{
		gomcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}
