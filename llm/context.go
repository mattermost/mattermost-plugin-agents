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

	// ResponseFilesSupported indicates the current flow attaches
	// ToolRuntime.CreatedFiles to the bot's streamed response post, so
	// file-creating response tools (CreateFile) may be cataloged. Only
	// conversation entry points that stream a response post set this.
	ResponseFilesSupported bool

	// SandboxFilesAttached indicates files the provider's code-execution
	// sandbox captured this turn are attached to the reply automatically. The
	// model needs to be told, because it cannot see the file ids the provider
	// reports and would otherwise paste file contents into its response text.
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

	// SandboxFileIDs records, in observation order, the provider-side ids of
	// files the code-execution sandbox captured this request (Anthropic
	// returns an id for every file a command left in $OUTPUT_DIR). The
	// response flow downloads these and attaches them to the reply, so the
	// order here is the order the files appear on the post.
	//
	// Ids only ever come from observed server-tool activity — never from model
	// input — so nothing the model says can add one.
	SandboxFileIDs []string

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

// AddCreatedFile records a file created by a tool during this turn so it can
// be attached to the response post. Files with an empty ID are skipped.
func (c *Context) AddCreatedFile(f CreatedFile) {
	if c == nil {
		return
	}
	c.ToolRuntime.AddCreatedFile(f)
}

func (t *ToolRuntimeContext) AddCreatedFile(f CreatedFile) {
	if t == nil || f.ID == "" {
		return
	}
	t.CreatedFiles = append(t.CreatedFiles, f)
}

// AddSandboxFileIDs records provider-side sandbox file ids observed in this
// request's server-tool activity, preserving arrival order. Empty and repeated
// ids are skipped — a server-tool snapshot is cumulative, so the same id is
// reported on every subsequent event of the turn.
func (c *Context) AddSandboxFileIDs(ids ...string) {
	if c == nil {
		return
	}
	c.ToolRuntime.AddSandboxFileIDs(ids...)
}

func (t *ToolRuntimeContext) AddSandboxFileIDs(ids ...string) {
	if t == nil {
		return
	}
	for _, id := range ids {
		if id == "" || slices.Contains(t.SandboxFileIDs, id) {
			continue
		}
		t.SandboxFileIDs = append(t.SandboxFileIDs, id)
	}
}

// ConsumeSandboxFileIDs returns the sandbox file ids observed this request, in
// observation order, and clears them. Consuming makes the attach path
// idempotent: a stream that ends twice (continuation, retry) must not attach
// the same provider file to the post again.
func (c *Context) ConsumeSandboxFileIDs() []string {
	if c == nil {
		return nil
	}
	ids := c.ToolRuntime.SandboxFileIDs
	c.ToolRuntime.SandboxFileIDs = nil
	return ids
}

// CreatedFilesList returns the files created by tools during this turn.
func (c *Context) CreatedFilesList() []CreatedFile {
	if c == nil {
		return nil
	}
	return c.ToolRuntime.CreatedFilesList()
}

func (t *ToolRuntimeContext) CreatedFilesList() []CreatedFile {
	if t == nil {
		return nil
	}
	return t.CreatedFiles
}

// SetResponseAttachmentBudget records how many more files the response post
// can hold, so file-creating tools reject excess calls before uploading.
func (c *Context) SetResponseAttachmentBudget(remaining int) {
	if c == nil {
		return
	}
	c.ToolRuntime.SetResponseAttachmentBudget(remaining)
}

func (t *ToolRuntimeContext) SetResponseAttachmentBudget(remaining int) {
	if t == nil {
		return
	}
	if remaining <= 0 {
		remaining = -1
	}
	t.ResponseAttachmentBudget = remaining
}

// ResponseAttachmentSlots returns how many more files response tools may
// create this turn: the recorded budget, or the full MaxPostAttachments
// budget when none was set.
func (c *Context) ResponseAttachmentSlots() int {
	if c == nil {
		return 0
	}
	return c.ToolRuntime.ResponseAttachmentSlots()
}

func (t *ToolRuntimeContext) ResponseAttachmentSlots() int {
	switch {
	case t == nil:
		return 0
	case t.ResponseAttachmentBudget == 0:
		return MaxPostAttachments
	case t.ResponseAttachmentBudget < 0:
		return 0
	default:
		return t.ResponseAttachmentBudget
	}
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
