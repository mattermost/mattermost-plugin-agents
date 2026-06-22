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

// Context represents the per-turn data necessary to build the context of the LLM.
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

	// ToolCatalog holds request-scoped inputs used while building the tool store.
	ToolCatalog ToolCatalogContext
	// ToolRuntime holds non-prompt tool execution state for this turn.
	ToolRuntime ToolRuntimeContext
}

// ToolCatalogContext holds request-scoped tool catalog build inputs.
type ToolCatalogContext struct {
	// MCPDynamicToolLoading indicates this context uses strict MCP dynamic loading.
	MCPDynamicToolLoading bool

	// DisabledMCPServerOrigins contains per-user disabled MCP server origins that
	// must be removed before strict registry construction.
	DisabledMCPServerOrigins []string

	// KeepMCPTool, when non-nil, is applied to MCP tools before strict registry
	// construction and before flag-off visible MCP insertion.
	KeepMCPTool func(Tool) bool

	// PreloadedMCPTools contains exact-or-bare MCP tool selectors for internal
	// predefined flows. They are selected only from the already-authorized MCP
	// catalog and are request scoped.
	PreloadedMCPTools []EnabledMCPTool

	// InteractiveUserPresent indicates the requesting user is interactively
	// present in Mattermost (DM or human channel mention) and can answer
	// pending tool approvals. Tools that require a user response (see
	// Tool.UserInteraction) are only cataloged when this is set.
	InteractiveUserPresent bool
}

// ToolRuntimeContext holds request-scoped tool runtime state that should not be
// rendered into the prompt.
type ToolRuntimeContext struct {
	// MCPDynamicToolTelemetry receives low-cardinality dynamic MCP tool events.
	MCPDynamicToolTelemetry                 MCPDynamicToolTelemetry
	MCPDynamicToolSearchUsed                bool
	MCPDynamicLoadedToolNames               map[string]bool
	MCPDynamicSearchLoadCallSuccessRecorded map[string]bool
}

type MCPDynamicToolTelemetry interface {
	ObserveMCPDynamicToolEvent(botName, event, result string)
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

func (c *Context) ObserveMCPDynamicToolEvent(event, result string) {
	if c == nil {
		return
	}

	botName := c.BotUsername
	if botName == "" {
		botName = c.BotName
	}
	if botName == "" {
		botName = "unknown"
	}

	c.ToolRuntime.ObserveMCPDynamicToolEvent(botName, event, result)
}

func (t *ToolRuntimeContext) ObserveMCPDynamicToolEvent(botName, event, result string) {
	if t == nil || t.MCPDynamicToolTelemetry == nil {
		return
	}

	t.MCPDynamicToolTelemetry.ObserveMCPDynamicToolEvent(botName, event, result)
}

func (c *Context) MarkMCPDynamicToolSearch() {
	if c == nil {
		return
	}
	c.ToolRuntime.MarkMCPDynamicToolSearch()
}

func (t *ToolRuntimeContext) MarkMCPDynamicToolSearch() {
	if t == nil {
		return
	}
	t.MCPDynamicToolSearchUsed = true
}

func (c *Context) MarkMCPDynamicToolLoaded(name string) {
	if c == nil {
		return
	}
	c.ToolRuntime.MarkMCPDynamicToolLoaded(name)
}

func (t *ToolRuntimeContext) MarkMCPDynamicToolLoaded(name string) {
	if t == nil || name == "" {
		return
	}
	if t.MCPDynamicLoadedToolNames == nil {
		t.MCPDynamicLoadedToolNames = make(map[string]bool)
	}
	t.MCPDynamicLoadedToolNames[name] = true
}

func (c *Context) ShouldRecordMCPDynamicSearchLoadCallSuccess(name string) bool {
	if c == nil {
		return false
	}
	return c.ToolRuntime.ShouldRecordMCPDynamicSearchLoadCallSuccess(name)
}

func (t *ToolRuntimeContext) ShouldRecordMCPDynamicSearchLoadCallSuccess(name string) bool {
	if t == nil || name == "" || !t.MCPDynamicToolSearchUsed || !t.MCPDynamicLoadedToolNames[name] {
		return false
	}
	if t.MCPDynamicSearchLoadCallSuccessRecorded == nil {
		t.MCPDynamicSearchLoadCallSuccessRecorded = make(map[string]bool)
	}
	if t.MCPDynamicSearchLoadCallSuccessRecorded[name] {
		return false
	}
	t.MCPDynamicSearchLoadCallSuccessRecorded[name] = true
	return true
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
