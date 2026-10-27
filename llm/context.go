// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"slices"
	"strings"
	"sync"
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

// UnconnectedMCPServer describes an MCP server the requesting user could use
// after connecting their own account to it. Every field comes from admin
// configuration.
type UnconnectedMCPServer struct {
	// Name is the admin-configured server name, the identifier a connection
	// request must use.
	Name         string
	ServerOrigin string
	Description  string
	Tools        []ToolInfo
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

	// ToolsScoped is set only by the scoped catalog: every tool in Tools reads
	// through this turn's scoped session or service-account credentials, never
	// the requester's. It is what lets the turn run tools without approval.
	ToolsScoped bool

	Tools *ToolStore
	// DisabledTools lists tools that run only in a DM or the Agents tab and so
	// cannot be called here. Set it through DisableTools so each tool appears
	// once.
	DisabledTools []ToolInfo
	// UnconnectedMCPServers lists servers the requester can connect from the
	// conversation; set only when the connection request tool is cataloged.
	UnconnectedMCPServers []UnconnectedMCPServer
	Parameters            map[string]any

	// ResponsePostID is the bot's streamed response post for this run. Set by
	// conversation entry points after the placeholder post exists; not
	// prompt-visible. Tools that resume the agent loop later
	// (wait_for_async_work) rederive all conversation state from this post.
	// Every flow that sets ToolCatalog.ResponseFilesSupported sets it before
	// tools can run, so tools gated on that flag may rely on it.
	ResponsePostID string

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

	// MCPServerOrigins maps configured remote MCP server names to their
	// origins so an answered connection request can find the server's tools.
	// It is nil where connection requests cannot be offered.
	MCPServerOrigins map[string]string

	// ResponseFilesSupported indicates the current flow attaches
	// ToolRuntime.CreatedFiles to the bot's streamed response post, so
	// file-creating response tools (CreateFile) may be cataloged. Only
	// conversation entry points that stream a response post set this.
	ResponseFilesSupported bool

	// SandboxFilesAttached is true when this turn's sandbox output will be
	// attached automatically. The model cannot see provider file ids, so the
	// prompt must tell it to copy shareable files into $OUTPUT_DIR.
	SandboxFilesAttached bool

	// ScopedTurn, when set, asks for this turn's tools to run on a scoped
	// session for the agent's bot. It only takes effect when the agent's
	// scoped channel mode is active in the Context's channel.
	ScopedTurn *ScopedTurn

	// ScopedPreview marks a catalog built only to show what a scoped turn
	// would carry: no session is minted and no tool runs.
	ScopedPreview bool
}

// ScopedTurn identifies the channel-triggered run a scoped session is minted
// for. The channel and requester come from the Context itself.
type ScopedTurn struct {
	TriggerPostID  string
	ThreadRootID   string
	ConversationID string
	// ConversationPending marks a turn whose conversation is created after
	// its context is built. The session's grant audit record then waits for
	// SetScopedTurnConversation, or for the session's release.
	ConversationPending bool
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

	scopedSession *scopedSessionHandle
}

type scopedSessionHandle struct {
	once        sync.Once
	release     func()
	released    func() bool
	grantOnce   sync.Once
	recordGrant func(conversationID string)
}

// SetScopedSession records how to end this turn's scoped session and how to
// tell whether it has ended, including when something other than this turn
// ended it. It is called while the context is built, before the turn runs.
// A non-nil recordGrant emits the session's grant audit record once the
// turn's conversation is known; see ScopedTurn.ConversationPending.
func (c *Context) SetScopedSession(release func(), released func() bool, recordGrant func(conversationID string)) {
	if c == nil || release == nil || released == nil {
		return
	}
	c.ToolRuntime.scopedSession = &scopedSessionHandle{release: release, released: released, recordGrant: recordGrant}
}

// SetScopedTurnConversation supplies the conversation of a turn whose
// context was built before the conversation existed.
func (c *Context) SetScopedTurnConversation(conversationID string) {
	if c == nil || c.ToolCatalog.ScopedTurn == nil {
		return
	}
	c.ToolCatalog.ScopedTurn.ConversationID = conversationID
	c.flushScopedGrant()
}

func (c *Context) flushScopedGrant() {
	handle := c.ToolRuntime.scopedSession
	if handle == nil || handle.recordGrant == nil {
		return
	}
	conversationID := ""
	if c.ToolCatalog.ScopedTurn != nil {
		conversationID = c.ToolCatalog.ScopedTurn.ConversationID
	}
	handle.grantOnce.Do(func() { handle.recordGrant(conversationID) })
}

// ReleaseScopedSession ends this turn's scoped session, if it has one. Every
// entry point that builds a scoped turn calls it when the run ends, on success
// and on error. It is safe to call more than once and from any goroutine.
func (c *Context) ReleaseScopedSession() {
	if c == nil || c.ToolRuntime.scopedSession == nil {
		return
	}
	c.flushScopedGrant()
	handle := c.ToolRuntime.scopedSession
	handle.once.Do(handle.release)
}

// ScopedSessionReleased reports whether this turn had a scoped session that
// has since ended: released by the turn, or revoked because the agent's
// scoped mode lapsed. Tools must no longer run without approval then.
func (c *Context) ScopedSessionReleased() bool {
	if c == nil || c.ToolRuntime.scopedSession == nil {
		return false
	}
	return c.ToolRuntime.scopedSession.released()
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

// IsToolCallable reports whether the named tool is in the tool store and not
// listed in DisabledTools. Disabled tools may stay in the store for prompt
// awareness but are never sent to the provider, so prompts must not tell the
// model to call them.
func (c *Context) IsToolCallable(name string) bool {
	if c == nil || c.Tools == nil || c.Tools.GetTool(name) == nil {
		return false
	}
	return !c.isToolDisabled(name)
}

// CallableToolsInfo lists the tools the model can call, sorted by name.
func (c *Context) CallableToolsInfo() []ToolInfo {
	if c == nil || c.Tools == nil {
		return nil
	}
	infos := slices.DeleteFunc(c.Tools.GetToolsInfo(), func(info ToolInfo) bool {
		return c.isToolDisabled(info.Name)
	})
	slices.SortFunc(infos, func(a, b ToolInfo) int { return strings.Compare(a.Name, b.Name) })
	return infos
}

// DisableTools records tools as unavailable in the current context.
func (c *Context) DisableTools(infos ...ToolInfo) {
	for _, info := range infos {
		if !c.isToolDisabled(info.Name) {
			c.DisabledTools = append(c.DisabledTools, info)
		}
	}
}

func (c *Context) isToolDisabled(name string) bool {
	return slices.ContainsFunc(c.DisabledTools, func(tool ToolInfo) bool {
		return tool.Name == name
	})
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
