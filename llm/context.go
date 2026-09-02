// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"slices"
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

	// ToolAuthMode records the identity mode the tool catalog was built with
	// (ToolAuthModeUser or ToolAuthModeServiceAccount); consumed by token usage attribution.
	ToolAuthMode string

	Tools             *ToolStore
	DisabledToolsInfo []ToolInfo // Info about tools that are unavailable in the current context (e.g., DM-only tools in a channel)
	Parameters        map[string]any

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

	// ResponseFilesSupported indicates the current flow attaches
	// ToolRuntime.CreatedFiles to the bot's streamed response post, so
	// file-creating response tools (CreateFile) may be cataloged. Only
	// conversation entry points that stream a response post set this.
	ResponseFilesSupported bool

	// SandboxFilesAttached is true when this turn's sandbox output will be
	// attached automatically. The model cannot see provider file ids, so the
	// prompt must tell it to copy shareable files into $OUTPUT_DIR.
	SandboxFilesAttached bool
}

// CreatedFile identifies a file created by a tool during this turn for
// attachment to the response post.
type CreatedFile struct {
	ID   string
	Name string
}

// ToolRuntimeContext holds request-scoped tool runtime state that should not be
// rendered into the prompt.
type ToolRuntimeContext struct {
	// MCPDynamicToolTelemetry receives low-cardinality dynamic MCP tool events.
	MCPDynamicToolTelemetry                 MCPDynamicToolTelemetry
	MCPDynamicToolSearchUsed                bool
	MCPDynamicLoadedToolNames               map[string]bool
	MCPDynamicSearchLoadCallSuccessRecorded map[string]bool

	// CreatedFiles collects files created by tools during this turn for
	// attachment to the response post.
	CreatedFiles []CreatedFile

	// SandboxFiles are provider files observed this request, in order, with
	// the route that produced them. Populated only from server-tool activity,
	// never from model input.
	SandboxFiles []ProviderFileReference

	// ResponseAttachmentBudget caps how many files response tools may create
	// this turn. 0 means unset (full MaxPostAttachments budget); -1 means the
	// response post has no room left. Set via SetResponseAttachmentBudget.
	ResponseAttachmentBudget int
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
	if c == nil || c.ToolRuntime.MCPDynamicToolTelemetry == nil {
		return
	}

	botName := c.BotUsername
	if botName == "" {
		botName = c.BotName
	}
	if botName == "" {
		botName = "unknown"
	}

	c.ToolRuntime.MCPDynamicToolTelemetry.ObserveMCPDynamicToolEvent(botName, event, result)
}

func (c *Context) MarkMCPDynamicToolSearch() {
	if c == nil {
		return
	}
	c.ToolRuntime.MCPDynamicToolSearchUsed = true
}

func (c *Context) MarkMCPDynamicToolLoaded(name string) {
	if c == nil || name == "" {
		return
	}
	if c.ToolRuntime.MCPDynamicLoadedToolNames == nil {
		c.ToolRuntime.MCPDynamicLoadedToolNames = make(map[string]bool)
	}
	c.ToolRuntime.MCPDynamicLoadedToolNames[name] = true
}

func (c *Context) ShouldRecordMCPDynamicSearchLoadCallSuccess(name string) bool {
	if c == nil || name == "" || !c.ToolRuntime.MCPDynamicToolSearchUsed || !c.ToolRuntime.MCPDynamicLoadedToolNames[name] {
		return false
	}
	if c.ToolRuntime.MCPDynamicSearchLoadCallSuccessRecorded == nil {
		c.ToolRuntime.MCPDynamicSearchLoadCallSuccessRecorded = make(map[string]bool)
	}
	if c.ToolRuntime.MCPDynamicSearchLoadCallSuccessRecorded[name] {
		return false
	}
	c.ToolRuntime.MCPDynamicSearchLoadCallSuccessRecorded[name] = true
	return true
}

// AddCreatedFile records a file created by a tool during this turn so it can
// be attached to the response post. Files with an empty ID are skipped.
func (c *Context) AddCreatedFile(f CreatedFile) {
	if c == nil || f.ID == "" {
		return
	}
	c.ToolRuntime.CreatedFiles = append(c.ToolRuntime.CreatedFiles, f)
}

// AddSandboxFiles records observed sandbox files in arrival order. Empty
// references and repeated route/id pairs are skipped because snapshots are cumulative.
func (c *Context) AddSandboxFiles(refs ...ProviderFileReference) {
	if c == nil {
		return
	}
	c.ToolRuntime.AddSandboxFiles(refs...)
}

func (t *ToolRuntimeContext) AddSandboxFiles(refs ...ProviderFileReference) {
	if t == nil {
		return
	}
	for _, ref := range refs {
		if ref.ID == "" || slices.ContainsFunc(t.SandboxFiles, func(existing ProviderFileReference) bool {
			return existing.ID == ref.ID && existing.ProviderRoute == ref.ProviderRoute
		}) {
			continue
		}
		t.SandboxFiles = append(t.SandboxFiles, ref)
	}
}

// ConsumeSandboxFiles returns observed files and clears them so a stream that
// ends twice cannot attach the same provider file again.
func (c *Context) ConsumeSandboxFiles() []ProviderFileReference {
	if c == nil {
		return nil
	}
	refs := c.ToolRuntime.SandboxFiles
	c.ToolRuntime.SandboxFiles = nil
	return refs
}

// CreatedFilesList returns the files created by tools during this turn.
func (c *Context) CreatedFilesList() []CreatedFile {
	if c == nil {
		return nil
	}
	return c.ToolRuntime.CreatedFiles
}

// SetResponseAttachmentBudget records how many more files the response post
// can hold, so file-creating tools reject excess calls before uploading.
func (c *Context) SetResponseAttachmentBudget(remaining int) {
	if c == nil {
		return
	}
	if remaining <= 0 {
		remaining = -1
	}
	c.ToolRuntime.ResponseAttachmentBudget = remaining
}

// ResponseAttachmentSlots returns how many more files response tools may
// create this turn: the recorded budget, or the full MaxPostAttachments
// budget when none was set.
func (c *Context) ResponseAttachmentSlots() int {
	switch {
	case c == nil:
		return 0
	case c.ToolRuntime.ResponseAttachmentBudget == 0:
		return MaxPostAttachments
	case c.ToolRuntime.ResponseAttachmentBudget < 0:
		return 0
	default:
		return c.ToolRuntime.ResponseAttachmentBudget
	}
}

func (c Context) String() string {
	var result strings.Builder
	fmt.Fprintf(&result, "Time: %v\nServerName: %v\nCompanyName: %v", c.Time, c.ServerName, c.CompanyName)
	if c.RequestingUser != nil {
		fmt.Fprintf(&result, "\nRequestingUser: %v", c.RequestingUser.Username)
	}
	if c.Channel != nil {
		fmt.Fprintf(&result, "\nChannel: %v", c.Channel.Name)
	}
	if c.Team != nil {
		fmt.Fprintf(&result, "\nTeam: %v", c.Team.Name)
	}

	result.WriteString("\n--- Parameters ---\n")
	for key := range c.Parameters {
		fmt.Fprintf(&result, " %v", key)
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
