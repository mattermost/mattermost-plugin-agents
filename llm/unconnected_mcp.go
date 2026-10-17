// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"strings"
	"unicode"
)

// ConnectMCPServerToolName is the runtime name of the built-in tool that asks
// the requesting user to connect an MCP server they have not connected yet.
const ConnectMCPServerToolName = "ConnectMCPServer"

const (
	// MaxUnconnectedMCPServerToolNames bounds how many admin-configured tool
	// names are advertised per unconnected server.
	MaxUnconnectedMCPServerToolNames = 15

	maxUnconnectedMCPServerDescriptionRunes = 300
	maxUnconnectedMCPServerToolNameRunes    = 100
)

// UnconnectedMCPServer is an MCP server the requesting user could use in this
// context but has not connected (never authorized, or the grant lapsed). All
// fields come from admin configuration and the server-built OAuth start URL.
type UnconnectedMCPServer struct {
	// Name is the server name exactly as configured; it is the identifier the
	// ConnectMCPServer tool and the webapp agree on.
	Name        string
	Origin      string
	Description string
	AuthURL     string
	ToolNames   []string
}

// PromptLine renders the server as one line of model-facing text. Every
// field is admin-authored and already single-line (see NewUnconnectedMCPServer).
func (s UnconnectedMCPServer) PromptLine() string {
	line := s.Name
	if s.Description != "" {
		line += ": " + s.Description
	}
	if len(s.ToolNames) > 0 {
		line += " (tools include: " + strings.Join(s.ToolNames, ", ") + ")"
	}
	return line
}

// NewUnconnectedMCPServer converts an OAuth-needed connect failure into an
// advertisable server. It reports false when the server cannot be connected
// from the UI (no start URL) or its name cannot be rendered safely on one line.
func NewUnconnectedMCPServer(authErr ToolAuthError) (UnconnectedMCPServer, bool) {
	name := authErr.ServerName
	if strings.TrimSpace(name) == "" || strings.ContainsFunc(name, unicode.IsControl) {
		return UnconnectedMCPServer{}, false
	}
	if strings.TrimSpace(authErr.AuthURL) == "" {
		return UnconnectedMCPServer{}, false
	}

	toolNames := make([]string, 0, min(len(authErr.ToolNames), MaxUnconnectedMCPServerToolNames))
	seen := make(map[string]bool, len(authErr.ToolNames))
	for _, toolName := range authErr.ToolNames {
		if len(toolNames) == MaxUnconnectedMCPServerToolNames {
			break
		}
		toolName = singleLinePromptText(toolName, maxUnconnectedMCPServerToolNameRunes)
		if toolName == "" || seen[toolName] {
			continue
		}
		seen[toolName] = true
		toolNames = append(toolNames, toolName)
	}

	return UnconnectedMCPServer{
		Name:        name,
		Origin:      NormalizeMCPServerOrigin(authErr.ServerOrigin),
		Description: singleLinePromptText(authErr.ServerDescription, maxUnconnectedMCPServerDescriptionRunes),
		AuthURL:     authErr.AuthURL,
		ToolNames:   toolNames,
	}, true
}

// singleLinePromptText collapses whitespace and drops control characters so
// admin-authored text cannot break out of its prompt list item, then bounds
// its length.
func singleLinePromptText(s string, maxRunes int) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	runes := 0
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
			continue
		case unicode.IsControl(r):
			continue
		}
		if runes >= maxRunes {
			break
		}
		if pendingSpace {
			b.WriteRune(' ')
			runes++
			pendingSpace = false
		}
		b.WriteRune(r)
		runes++
	}
	return SanitizeNonPrintableChars(strings.TrimSpace(b.String()))
}

// UnconnectedMCPServerByName returns this turn's unconnected server with the
// given configured name.
func (c *Context) UnconnectedMCPServerByName(name string) (UnconnectedMCPServer, bool) {
	if c == nil {
		return UnconnectedMCPServer{}, false
	}
	for _, server := range c.ToolCatalog.UnconnectedMCPServers {
		if server.Name == name {
			return server, true
		}
	}
	return UnconnectedMCPServer{}, false
}
