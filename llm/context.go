// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// ToolInfo represents basic information about a tool without its full implementation.
// Used to inform LLMs about tools that are unavailable in the current context.
type ToolInfo struct {
	Name         string
	Description  string
	ServerOrigin string
}

// MCPInvocationContext holds per-run data for MCP tool execution.
type MCPInvocationContext struct {
	// ServerMetadata maps MCP server origin key (e.g. embedded client key) to opaque metadata
	// forwarded on each tool call for that server.
	ServerMetadata map[string]map[string]any
}

// Context represents the data necessary to build the context of the LLM.
// For consumers none of the fields can be assumed to be present.
type Context struct {
	// Server
	Time        string
	ServerName  string
	CompanyName string
	SiteURL     string

	// Location
	Team    *model.Team
	Channel *model.Channel
	Thread  []Post // Normalized posts that already have been formatted. nil if not in a thread or a root post

	// User that is making the request
	RequestingUser *model.User

	// Bot Specific
	BotName            string
	BotUsername        string
	BotUserID          string
	BotModel           string
	BotServiceType     string
	CustomInstructions string

	Tools             *ToolStore
	DisabledToolsInfo []ToolInfo // Info about tools that are unavailable in the current context (e.g., DM-only tools in a channel)
	Parameters        map[string]interface{}

	MCPInvocation *MCPInvocationContext
}

// ContextOption defines a function that configures a Context
type ContextOption func(*Context)

// NewContext creates a new Context with the given options
func NewContext(opts ...ContextOption) *Context {
	c := &Context{
		Time: time.Now().UTC().Format(time.RFC1123),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// SetBotFields populates bot-related context fields from config and service values.
// This avoids duplicating bot field assignment across multiple packages.
func (c *Context) SetBotFields(displayName, username, userID, defaultModel, serviceType, customInstructions string) {
	c.BotName = displayName
	c.BotUsername = username
	c.BotUserID = userID
	c.BotModel = defaultModel
	c.BotServiceType = serviceType
	c.CustomInstructions = customInstructions
}

// SetMCPServerMetadata stores run-scoped MCP metadata for a single server origin.
// Passing nil metadata removes any existing entry for that server.
func (c *Context) SetMCPServerMetadata(server string, metadata map[string]any) {
	if c == nil || server == "" {
		return
	}

	if metadata == nil {
		if c.MCPInvocation == nil || c.MCPInvocation.ServerMetadata == nil {
			return
		}
		delete(c.MCPInvocation.ServerMetadata, server)
		return
	}

	if c.MCPInvocation == nil {
		c.MCPInvocation = &MCPInvocationContext{}
	}
	if c.MCPInvocation.ServerMetadata == nil {
		c.MCPInvocation.ServerMetadata = make(map[string]map[string]any)
	}
	c.MCPInvocation.ServerMetadata[server] = cloneMetadataMap(metadata)
}

// GetMCPServerMetadata returns a copy of the run-scoped MCP metadata for a server origin.
func (c *Context) GetMCPServerMetadata(server string) map[string]any {
	if c == nil || server == "" || c.MCPInvocation == nil || c.MCPInvocation.ServerMetadata == nil {
		return nil
	}

	metadata, ok := c.MCPInvocation.ServerMetadata[server]
	if !ok {
		return nil
	}
	return cloneMetadataMap(metadata)
}

func cloneMetadataMap(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}

	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

// CustomPromptVars returns a flat map of whitelisted variables for use in
// user-created custom prompt templates. Only safe, useful fields are exposed.
func (c *Context) CustomPromptVars() map[string]string {
	vars := map[string]string{
		"Time":    c.Time,
		"BotName": c.BotName,
	}
	if c.RequestingUser != nil {
		vars["Username"] = c.RequestingUser.Username
		vars["FirstName"] = c.RequestingUser.FirstName
		vars["LastName"] = c.RequestingUser.LastName
	}
	if c.Channel != nil {
		vars["Channel"] = c.Channel.DisplayName
		vars["ChannelName"] = c.Channel.Name
	}
	if c.Team != nil {
		vars["Team"] = c.Team.DisplayName
		vars["TeamName"] = c.Team.Name
	}
	return vars
}

func (c Context) String() string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Time: %v\nServerName: %v\nCompanyName: %v", c.Time, c.ServerName, c.CompanyName))
	if c.RequestingUser != nil {
		result.WriteString(fmt.Sprintf("\nRequestingUser: %v", c.RequestingUser.Username))
	}
	if c.Channel != nil {
		result.WriteString(fmt.Sprintf("\nChannel: %v", c.Channel.Name))
	}
	if c.Team != nil {
		result.WriteString(fmt.Sprintf("\nTeam: %v", c.Team.Name))
	}

	result.WriteString("\n--- Parameters ---\n")
	for key := range c.Parameters {
		result.WriteString(fmt.Sprintf(" %v", key))
	}

	if c.Tools != nil {
		result.WriteString("\n--- Tools ---\n")
		for _, tool := range c.Tools.GetTools() {
			result.WriteString(tool.Name)
			result.WriteString(" ")
		}
	}

	return result.String()
}
