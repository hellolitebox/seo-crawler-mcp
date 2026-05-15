package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// registerPrompts adds all prompt handlers.
func (s *Server) registerPrompts() {
	s.mcpServer.AddPrompt(gomcp.NewPrompt(
		"analyze_technical_seo",
		gomcp.WithPromptDescription("Analyze technical SEO for a completed crawl"),
		gomcp.WithArgument("jobId", gomcp.RequiredArgument(), gomcp.ArgumentDescription("Crawl job ID")),
	), s.handleAnalyzeSEOPrompt)

	s.mcpServer.AddPrompt(gomcp.NewPrompt(
		"investigate_url",
		gomcp.WithPromptDescription("Investigate a specific URL from a crawl"),
		gomcp.WithArgument("jobId", gomcp.RequiredArgument(), gomcp.ArgumentDescription("Crawl job ID")),
		gomcp.WithArgument("url", gomcp.RequiredArgument(), gomcp.ArgumentDescription("URL to investigate")),
	), s.handleInvestigateURLPrompt)
}

// handleAnalyzeSEOPrompt returns a prompt with the crawl summary embedded.
func (s *Server) handleAnalyzeSEOPrompt(
	ctx context.Context,
	req gomcp.GetPromptRequest,
) (*gomcp.GetPromptResult, error) {
	jobID := req.Params.Arguments["jobId"]
	if jobID == "" {
		return nil, fmt.Errorf("missing required argument %q", "jobId")
	}

	summary, err := s.db.GetCrawlSummary(jobID)
	if err != nil {
		return nil, fmt.Errorf("getting summary for job %q: %w", jobID, err)
	}

	summaryJSON, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling summary: %w", err)
	}

	text := fmt.Sprintf(
		"Analyze the technical SEO of this crawl:\n\n%s\n\n"+
			"Focus on: critical issues, duplicate content, orphan pages, canonical problems, indexability. "+
			"Provide actionable recommendations prioritized by impact.",
		string(summaryJSON),
	)

	return &gomcp.GetPromptResult{
		Description: "Technical SEO analysis for crawl " + jobID,
		Messages: []gomcp.PromptMessage{
			{
				Role: gomcp.RoleUser,
				Content: gomcp.TextContent{
					Type: "text",
					Text: text,
				},
			},
		},
	}, nil
}

// investigateURLData holds the combined data for URL investigation.
type investigateURLData struct {
	URL    any   `json:"url"`
	Page   any   `json:"page,omitempty"`
	Issues []any `json:"issues"`
	Edges  []any `json:"edges"`
}

// handleInvestigateURLPrompt returns a prompt with page data embedded.
func (s *Server) handleInvestigateURLPrompt(
	ctx context.Context,
	req gomcp.GetPromptRequest,
) (*gomcp.GetPromptResult, error) {
	jobID := req.Params.Arguments["jobId"]
	if jobID == "" {
		return nil, fmt.Errorf("missing required argument %q", "jobId")
	}

	targetURL := req.Params.Arguments["url"]
	if targetURL == "" {
		return nil, fmt.Errorf("missing required argument %q", "url")
	}

	// Look up the URL record
	urlRec, err := s.db.GetURLByNormalized(jobID, targetURL)
	if err != nil {
		return nil, fmt.Errorf("looking up URL %q in job %q: %w", targetURL, jobID, err)
	}

	// Build investigation data
	var investigation investigateURLData
	investigation.URL = urlRec

	// Get page data if available
	page, err := s.db.GetPageByURL(jobID, urlRec.ID)
	if err == nil && page != nil {
		investigation.Page = page
	}

	// Get issues for this URL
	issues, err := s.db.GetIssuesByJob(jobID, 500, "")
	if err == nil {
		for _, issue := range issues {
			if issue.URLID.Valid && issue.URLID.Int64 == urlRec.ID {
				investigation.Issues = append(investigation.Issues, issue)
			}
		}
	}
	if investigation.Issues == nil {
		investigation.Issues = []any{}
	}

	// Get outbound edges
	edges, err := s.db.GetEdgesBySource(jobID, urlRec.ID, 100, "")
	if err == nil {
		for _, e := range edges {
			investigation.Edges = append(investigation.Edges, e)
		}
	}
	if investigation.Edges == nil {
		investigation.Edges = []any{}
	}

	dataJSON, err := json.MarshalIndent(investigation, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling investigation data: %w", err)
	}

	text := fmt.Sprintf(
		"Investigate this URL from the crawl:\n\nURL: %s\n\n%s\n\n"+
			"Analyze: indexability, canonical setup, link profile, content quality, structured data. "+
			"Flag any issues and suggest fixes.",
		targetURL,
		string(dataJSON),
	)

	return &gomcp.GetPromptResult{
		Description: "URL investigation for " + targetURL,
		Messages: []gomcp.PromptMessage{
			{
				Role: gomcp.RoleUser,
				Content: gomcp.TextContent{
					Type: "text",
					Text: text,
				},
			},
		},
	}, nil
}
