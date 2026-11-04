// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Descriptions of unconnected servers are rendered into the connection request
// tool's definition, so every field is flattened to one line and capped.
const (
	maxToolNameRunes       = 100
	maxKnownToolDescRunes  = 200
	maxKnownToolsPerServer = 30
)

// promptSafeText escapes invisible characters, collapses all whitespace
// (including newlines) to single spaces, and truncates to maxRunes.
func promptSafeText(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(llm.SanitizeNonPrintableChars(s)), " ")
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

// DescribeUnconnectedMCPServers returns what admins configured about each
// remote server named in authErrors: its name, description, and tools. Nothing
// a server reported to another user's connection is included.
func (m *ClientManager) DescribeUnconnectedMCPServers(authErrors []llm.ToolAuthError) []llm.UnconnectedMCPServer {
	if len(authErrors) == 0 {
		return nil
	}
	cfg, _ := m.snapshotRuntime()

	described := make([]llm.UnconnectedMCPServer, 0, len(authErrors))
	seen := make(map[string]bool, len(authErrors))
	for _, authErr := range authErrors {
		if authErr.ServerName == "" || seen[authErr.ServerName] {
			continue
		}
		seen[authErr.ServerName] = true

		server := findRemoteServer(cfg, authErr.ServerName, authErr.ServerOrigin)
		if server == nil {
			described = append(described, llm.UnconnectedMCPServer{
				Name:         authErr.ServerName,
				ServerOrigin: authErr.ServerOrigin,
			})
			continue
		}
		described = append(described, describeConfiguredServer(server))
	}
	return described
}

// describeConfiguredServer describes a server the user must connect from its
// admin configuration alone.
func describeConfiguredServer(server *ServerConfig) llm.UnconnectedMCPServer {
	return llm.UnconnectedMCPServer{
		Name:         server.Name,
		ServerOrigin: server.BaseURL,
		Description:  promptSafeText(server.Description, config.MaxMCPServerDescriptionRunes),
		Tools:        configuredServerTools(server),
	}
}

// MCPServerOriginsByName maps each enabled remote server's configured name to
// its origin.
func (m *ClientManager) MCPServerOriginsByName() map[string]string {
	cfg, _ := m.snapshotRuntime()
	origins := make(map[string]string, len(cfg.Servers))
	for _, server := range cfg.Servers {
		if server.Enabled && server.Name != "" && server.BaseURL != "" {
			origins[server.Name] = server.BaseURL
		}
	}
	return origins
}

func findRemoteServer(cfg Config, name, origin string) *ServerConfig {
	for i := range cfg.Servers {
		server := &cfg.Servers[i]
		if server.Enabled && server.Name == name && server.BaseURL == origin {
			return server
		}
	}
	return nil
}

// configuredServerTools lists the tools the admin configured on a server,
// with their admin-written descriptions. Admin-disabled tools are left out.
func configuredServerTools(server *ServerConfig) []llm.ToolInfo {
	var tools []llm.ToolInfo
	for _, toolConfig := range server.ToolConfigs {
		if _, enabled := server.GetToolPolicy(toolConfig.Name); !enabled {
			continue
		}
		tools = append(tools, llm.ToolInfo{
			Name:         promptSafeText(toolConfig.Name, maxToolNameRunes),
			Description:  promptSafeText(toolConfig.RetrievalDescriptionOverride, maxKnownToolDescRunes),
			ServerOrigin: server.BaseURL,
		})
	}

	slices.SortFunc(tools, func(a, b llm.ToolInfo) int { return cmp.Compare(a.Name, b.Name) })
	tools = slices.CompactFunc(tools, func(a, b llm.ToolInfo) bool { return a.Name == b.Name })
	if len(tools) > maxKnownToolsPerServer {
		tools = tools[:maxKnownToolsPerServer]
	}
	return tools
}

// DescribeDMOnlyMCPServers names the remote servers selection leaves out only
// because they are limited to direct messages, so the model can tell the
// requester to move to a DM. A server the requester could not use there
// either (access policy, agent allowlist, license, service-account headers)
// is not named.
func (m *ClientManager) DescribeDMOnlyMCPServers(ctx context.Context, req CatalogRequest, selection ToolSelection) []llm.DMOnlyMCPServer {
	if !selection.ExcludeDMOnlyServers || req.validate() != nil {
		return nil
	}
	cfg, embeddedClient := m.snapshotRuntime()
	// Access policy is evaluated for the DM-only candidates alone rather than
	// for every configured server.
	candidates := m.resolveEligibleServers(cfg, embeddedClient, nil, selection, nil, req.ServiceAccount).dmOnly

	var described []llm.DMOnlyMCPServer
	for _, server := range candidates {
		if !m.canUseServer(ctx, req.InvokingUserID, server.ID, server.Name) {
			continue
		}
		described = append(described, llm.DMOnlyMCPServer{
			Name:        promptSafeText(server.Name, maxToolNameRunes),
			Description: promptSafeText(server.Description, config.MaxMCPServerDescriptionRunes),
		})
	}
	return described
}
