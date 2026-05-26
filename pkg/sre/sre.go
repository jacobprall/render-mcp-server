package sre

import (
	"github.com/mark3labs/mcp-go/server"
	"github.com/render-oss/render-mcp-server/pkg/client"
)

func Tools(c *client.ClientWithResponses) []server.ServerTool {
	return []server.ServerTool{
		renderGetTool(c),
		renderActTool(c),
		renderQueryTool(c),
	}
}

func AddResources(s *server.MCPServer, c *client.ClientWithResponses) {
	addInventoryResource(s, c)
}
