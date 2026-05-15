package mcp

import (
	"context"
	"fmt"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/dto"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) handleGetCrawlSummary(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	jobID, ok := args["jobId"].(string)
	if !ok || jobID == "" {
		return gomcp.NewToolResultError("parameter \"jobId\" is required"), nil
	}

	if s.db == nil {
		return gomcp.NewToolResultError("server not configured: database unavailable"), nil
	}

	summary, err := s.db.GetCrawlSummary(jobID)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("getting summary for job %q: %v", jobID, err)), nil
	}

	return gomcp.NewToolResultJSON(summary)
}

// clampLimit constrains limit to [1, max] with a default.
func clampLimit(raw float64, defaultVal, maxVal int) int {
	if raw <= 0 {
		return defaultVal
	}
	v := int(raw)
	if v > maxVal {
		return maxVal
	}
	return v
}

// buildFilter extracts QueryFilter fields from request arguments.
func buildFilter(args map[string]any) storage.QueryFilter {
	var f storage.QueryFilter

	if v, ok := args["issueType"].(string); ok {
		f.IssueType = v
	}
	if v, ok := args["statusCodeFamily"].(string); ok {
		f.StatusCodeFamily = v
	}
	if v, ok := args["urlPattern"].(string); ok {
		f.URLPattern = v
	}
	if v, ok := args["urlGroup"].(string); ok {
		f.URLGroup = v
	}
	if v, ok := args["minDepth"].(float64); ok {
		d := int(v)
		f.MinDepth = &d
	}
	if v, ok := args["maxDepth"].(float64); ok {
		d := int(v)
		f.MaxDepth = &d
	}
	if v, ok := args["isInternal"].(bool); ok {
		f.IsInternal = &v
	}
	if v, ok := args["relationType"].(string); ok {
		f.RelationType = v
	}
	if v, ok := args["contentType"].(string); ok {
		f.ContentType = v
	}
	if v, ok := args["clusterType"].(string); ok {
		f.ClusterType = v
	}
	if v, ok := args["targetDomain"].(string); ok {
		f.TargetDomain = v
	}

	return f
}

// urlLookup returns a dto.URLLookup backed by the database.
func (s *Server) urlLookup() dto.URLLookup {
	return func(id int64) string {
		u, err := s.db.GetURL(id)
		if err != nil || u == nil {
			return fmt.Sprintf("url:%d", id)
		}
		return u.NormalizedURL
	}
}

func (s *Server) handleGetCrawlResults(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	jobID, ok := args["jobId"].(string)
	if !ok || jobID == "" {
		return gomcp.NewToolResultError("parameter \"jobId\" is required"), nil
	}

	if s.db == nil {
		return gomcp.NewToolResultError("server not configured: database unavailable"), nil
	}

	view := "pages"
	if v, ok := args["view"].(string); ok && v != "" {
		view = v
	}

	var rawLimit float64
	if l, ok := args["limit"].(float64); ok {
		rawLimit = l
	}
	limit := clampLimit(rawLimit, 50, 500)

	var cursor string
	if c, ok := args["cursor"].(string); ok {
		cursor = c
	}

	filter := buildFilter(args)

	switch view {
	case "pages":
		result, err := s.db.QueryPages(jobID, filter, cursor, limit)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying pages for job %q: %v", jobID, err)), nil
		}
		lookup := s.urlLookup()
		dtos := make([]dto.PageDTO, 0, len(result.Results))
		for _, p := range result.Results {
			dtos = append(dtos, dto.PageFromStorage(p, lookup))
		}
		return gomcp.NewToolResultJSON(map[string]any{
			"results":        dtos,
			"nextCursor":     result.NextCursor,
			"totalCount":     result.TotalCount,
			"ignoredFilters": result.IgnoredFilters,
		})

	case "issues":
		result, err := s.db.QueryIssues(jobID, filter, cursor, limit)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying issues for job %q: %v", jobID, err)), nil
		}
		lookup := s.urlLookup()
		dtos := make([]dto.IssueDTO, 0, len(result.Results))
		for _, i := range result.Results {
			dtos = append(dtos, dto.IssueFromStorage(i, lookup))
		}
		return gomcp.NewToolResultJSON(map[string]any{
			"results":        dtos,
			"nextCursor":     result.NextCursor,
			"totalCount":     result.TotalCount,
			"ignoredFilters": result.IgnoredFilters,
		})

	case "external_links":
		// Force isInternal=false for external links view.
		isExternal := false
		filter.IsInternal = &isExternal
		result, err := s.db.QueryEdgesView(jobID, filter, cursor, limit)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying external links for job %q: %v", jobID, err)), nil
		}
		lookup := s.urlLookup()
		dtos := make([]dto.EdgeDTO, 0, len(result.Results))
		for _, e := range result.Results {
			dtos = append(dtos, dto.EdgeFromStorage(e, lookup))
		}
		return gomcp.NewToolResultJSON(map[string]any{
			"results":        dtos,
			"nextCursor":     result.NextCursor,
			"totalCount":     result.TotalCount,
			"ignoredFilters": result.IgnoredFilters,
		})

	case "response_codes":
		result, err := s.db.QueryResponseCodes(jobID, filter, cursor, limit)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying response codes for job %q: %v", jobID, err)), nil
		}
		lookup := s.urlLookup()
		dtos := make([]dto.FetchDTO, 0, len(result.Results))
		for _, f := range result.Results {
			dtos = append(dtos, dto.FetchFromStorage(f, lookup))
		}
		return gomcp.NewToolResultJSON(map[string]any{
			"results":        dtos,
			"nextCursor":     result.NextCursor,
			"totalCount":     result.TotalCount,
			"ignoredFilters": result.IgnoredFilters,
		})

	default:
		return gomcp.NewToolResultError(fmt.Sprintf("unsupported view %q; use pages, issues, external_links, or response_codes", view)), nil
	}
}

func (s *Server) handleGetLinkGraph(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	jobID, ok := args["jobId"].(string)
	if !ok || jobID == "" {
		return gomcp.NewToolResultError("parameter \"jobId\" is required"), nil
	}

	if s.db == nil {
		return gomcp.NewToolResultError("server not configured: database unavailable"), nil
	}

	var rawLimit float64
	if l, ok := args["limit"].(float64); ok {
		rawLimit = l
	}
	limit := clampLimit(rawLimit, 50, 500)

	var cursor string
	if c, ok := args["cursor"].(string); ok {
		cursor = c
	}

	var urlID int64
	if uid, ok := args["urlId"].(float64); ok {
		urlID = int64(uid)
	}
	if urlID <= 0 {
		return gomcp.NewToolResultError("parameter \"urlId\" is required and must be > 0"), nil
	}

	direction := "outbound"
	if d, ok := args["direction"].(string); ok && d != "" {
		direction = d
	}

	switch direction {
	case "outbound", "inbound", "both":
		// valid
	default:
		return gomcp.NewToolResultError(fmt.Sprintf("unsupported direction %q; use outbound, inbound, or both", direction)), nil
	}

	lookup := s.urlLookup()

	// Collect results based on direction.
	type graphResult struct {
		Edges      []dto.EdgeDTO `json:"edges"`
		NextCursor string        `json:"nextCursor,omitempty"`
	}

	switch direction {
	case "outbound":
		edges, err := s.db.GetEdgesBySource(jobID, urlID, limit, cursor)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying outbound edges for job %q: %v", jobID, err)), nil
		}
		dtos := make([]dto.EdgeDTO, 0, len(edges))
		for _, e := range edges {
			dtos = append(dtos, dto.EdgeFromStorage(e, lookup))
		}
		var nextCursor string
		if len(edges) == limit {
			nextCursor = fmt.Sprintf("%d", edges[len(edges)-1].ID)
		}
		return gomcp.NewToolResultJSON(graphResult{Edges: dtos, NextCursor: nextCursor})

	case "inbound":
		edges, err := s.db.GetEdgesByTarget(jobID, urlID, limit, cursor)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying inbound edges for job %q: %v", jobID, err)), nil
		}
		dtos := make([]dto.EdgeDTO, 0, len(edges))
		for _, e := range edges {
			dtos = append(dtos, dto.EdgeFromStorage(e, lookup))
		}
		var nextCursor string
		if len(edges) == limit {
			nextCursor = fmt.Sprintf("%d", edges[len(edges)-1].ID)
		}
		return gomcp.NewToolResultJSON(graphResult{Edges: dtos, NextCursor: nextCursor})

	default: // "both"
		half := limit / 2
		if half < 1 {
			half = 1
		}

		outEdges, err := s.db.GetEdgesBySource(jobID, urlID, half, cursor)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying outbound edges for job %q: %v", jobID, err)), nil
		}
		inEdges, err := s.db.GetEdgesByTarget(jobID, urlID, half, cursor)
		if err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("querying inbound edges for job %q: %v", jobID, err)), nil
		}

		outDTOs := make([]dto.EdgeDTO, 0, len(outEdges))
		for _, e := range outEdges {
			outDTOs = append(outDTOs, dto.EdgeFromStorage(e, lookup))
		}
		inDTOs := make([]dto.EdgeDTO, 0, len(inEdges))
		for _, e := range inEdges {
			inDTOs = append(inDTOs, dto.EdgeFromStorage(e, lookup))
		}

		return gomcp.NewToolResultJSON(map[string]any{
			"outbound": outDTOs,
			"inbound":  inDTOs,
		})
	}
}
