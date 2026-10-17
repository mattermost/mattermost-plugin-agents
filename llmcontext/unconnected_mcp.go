// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"errors"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
)

// unconnectedMCPServers is the single eligibility policy for advertising MCP
// servers the requester has not connected, applied both when the catalog is
// built and when a server's authorization lapses mid-turn. Access policy,
// license, and origin selection already kept ineligible servers from being
// dialed; this applies the agent allowlist, user-disabled origins, and the
// context tool filter to each server's admin-configured tool names. A server
// whose configured tools are all filtered out is not advertised; one with no
// configured tools is advertised only when no context tool filter applies,
// since nothing is known about which of its tools the filter would keep.
func unconnectedMCPServers(authErrors []llm.ToolAuthError, catalog llm.ToolCatalogContext) []llm.UnconnectedMCPServer {
	scope := catalog.UnconnectedMCPServerScope
	if scope == nil || len(authErrors) == 0 {
		return nil
	}

	disabled := make(map[string]bool, len(catalog.DisabledMCPServerOrigins))
	for _, origin := range llm.NormalizeMCPServerOrigins(catalog.DisabledMCPServerOrigins) {
		disabled[origin] = true
	}

	keepTool := func(origin, name string) bool {
		if !scope.AllMCPTools && !llm.EnabledMCPToolsAllow(scope.EnabledMCPTools, origin, name) {
			return false
		}
		// KeepMCPTool takes a Tool, but an unconnected server's tools are
		// known only by their configured name and origin.
		return catalog.KeepMCPTool == nil || catalog.KeepMCPTool(llm.Tool{Name: name, ServerOrigin: origin})
	}

	var servers []llm.UnconnectedMCPServer
	seen := make(map[string]bool, len(authErrors))
	for _, authErr := range authErrors {
		origin := llm.NormalizeMCPServerOrigin(authErr.ServerOrigin)
		if disabled[origin] || seen[origin] {
			continue
		}
		if !scope.AllMCPTools && !allowlistIncludesServer(scope.EnabledMCPTools, origin) {
			continue
		}

		configured := authErr.ToolNames
		kept := slices.DeleteFunc(slices.Clone(configured), func(name string) bool {
			return !keepTool(origin, name)
		})
		if len(kept) == 0 && (len(configured) > 0 || catalog.KeepMCPTool != nil) {
			continue
		}

		authErr.ToolNames = kept
		server, ok := llm.NewUnconnectedMCPServer(authErr)
		if !ok {
			continue
		}
		seen[origin] = true
		servers = append(servers, server)
	}
	return servers
}

func allowlistIncludesServer(allowlist []llm.EnabledMCPTool, origin string) bool {
	return slices.ContainsFunc(allowlist, func(enabled llm.EnabledMCPTool) bool {
		return llm.NormalizeMCPServerOrigin(enabled.ServerOrigin) == origin
	})
}

// publishUnconnectedMCPServers records the turn's unconnected servers and
// catalogs ConnectMCPServer for them, replacing any earlier copy so its
// description and enum list exactly these servers.
func publishUnconnectedMCPServers(store *llm.ToolStore, c *llm.Context, servers []llm.UnconnectedMCPServer) {
	c.ToolCatalog.UnconnectedMCPServers = servers
	if store != nil && len(servers) > 0 {
		store.AddTools([]llm.Tool{mmtools.NewConnectMCPServerTool(servers)})
	}
}

// MCPToolErrorResult returns the tool result text for a failed tool call.
// When the call failed because the user's authorization for its MCP server
// lapsed, the server is admitted through the same policy as at catalog build
// and, if eligible, ConnectMCPServer is offered for it for the rest of the
// turn.
//
// It mutates c's catalog and tool store, so it must run on the goroutine that
// owns the tool loop, between tool calls, never from inside a resolver.
func MCPToolErrorResult(c *llm.Context, err error) string {
	needed, ok := errors.AsType[*mcp.ReconnectNeededError](err)
	if !ok || !offerReconnect(c, needed.Auth) {
		return err.Error()
	}
	return needed.ConnectGuidance()
}

func offerReconnect(c *llm.Context, authErr llm.ToolAuthError) bool {
	if c == nil {
		return false
	}
	if _, listed := c.UnconnectedMCPServerByName(authErr.ServerName); !listed {
		admitted := unconnectedMCPServers([]llm.ToolAuthError{authErr}, c.ToolCatalog)
		if len(admitted) == 0 {
			return false
		}
		publishUnconnectedMCPServers(c.Tools, c, append(slices.Clone(c.ToolCatalog.UnconnectedMCPServers), admitted...))
	}
	return c.IsToolCallable(llm.ConnectMCPServerToolName)
}
