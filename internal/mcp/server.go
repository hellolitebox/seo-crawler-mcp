// Package mcp provides the MCP server interface for the SEO crawler.
package mcp

import (
	"context"
	"sync"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/engine"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	gomcp "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Server wraps the MCP protocol server with SEO crawler dependencies.
type Server struct {
	db        *storage.DB
	engine    *engine.Engine
	fetcher   *fetcher.Fetcher
	config    *config.Config
	mcpServer *mcpserver.MCPServer
	runMu     sync.Mutex
	running   map[string]context.CancelCauseFunc
}

// ServerConfig holds all dependencies for the MCP server.
type ServerConfig struct {
	DB      *storage.DB
	Engine  *engine.Engine
	Fetcher *fetcher.Fetcher
	Config  *config.Config
}

// NewServer creates and configures a new MCP server with all tools registered.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		db:      cfg.DB,
		engine:  cfg.Engine,
		fetcher: cfg.Fetcher,
		config:  cfg.Config,
		running: map[string]context.CancelCauseFunc{},
	}

	mcpSrv := mcpserver.NewMCPServer(
		"seo-crawler-mcp",
		"0.1.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithResourceCapabilities(true, true),
		mcpserver.WithPromptCapabilities(true),
		mcpserver.WithLogging(),
	)

	s.mcpServer = mcpSrv
	s.registerTools()
	s.registerResources()
	s.registerPrompts()
	return s
}

// ServeStdio starts the MCP server on stdio transport.
func (s *Server) ServeStdio() error {
	return mcpserver.ServeStdio(s.mcpServer)
}

// MCPServer returns the underlying MCPServer for testing.
func (s *Server) MCPServer() *mcpserver.MCPServer {
	return s.mcpServer
}

// logInfo sends an info-level log message to the current client session.
// Errors are silently ignored (logging is best-effort).
func (s *Server) logInfo(ctx context.Context, message string) {
	notification := gomcp.NewLoggingMessageNotification(gomcp.LoggingLevelInfo, "seo-crawler", message)
	_ = s.mcpServer.SendLogMessageToClient(ctx, notification)
}
