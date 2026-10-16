// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"errors"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

// MCPToolErrorResult returns the tool result text for a failed tool call.
// When the call failed because the user's authorization for its MCP server
// lapsed, the server's remaining tools are removed for the rest of the turn,
// and where connection requests can be offered, RequestMCPConnection is
// offered for it.
//
// It changes c's tool store, so it must run on the goroutine that owns the
// tool loop, between tool calls, never from inside a resolver.
func MCPToolErrorResult(c *llm.Context, err error) string {
	needed, ok := errors.AsType[*mcp.ReconnectNeededError](err)
	if !ok {
		return err.Error()
	}
	if c == nil || c.Tools == nil {
		return needed.Error()
	}
	// The store holds exactly the server's tools this agent may use here, so
	// it filters the server's description the way catalog building would.
	server := needed.Server
	server.Tools = slices.DeleteFunc(slices.Clone(server.Tools), func(info llm.ToolInfo) bool {
		return !c.Tools.HasMCPToolFromOrigin(server.ServerOrigin, func(tool llm.Tool) bool {
			return llm.MCPToolNameMatches(tool.Name, info.Name)
		})
	})
	c.Tools.RemoveMCPToolsFromOrigin(server.ServerOrigin)
	if c.ToolCatalog.MCPServerOrigins == nil {
		return needed.Error()
	}

	listed := slices.ContainsFunc(c.UnconnectedMCPServers, func(listed llm.UnconnectedMCPServer) bool {
		return listed.Name == server.Name
	})
	if !listed {
		c.UnconnectedMCPServers = append(c.UnconnectedMCPServers, server)
	}
	c.Tools.AddTools([]llm.Tool{mcp.NewRequestConnectionTool(c.UnconnectedMCPServers)})
	return needed.ConnectGuidance()
}
