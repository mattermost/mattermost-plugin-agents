// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// Plugin MCP server origins the project binder knows.
const (
	BoardsPluginID    = "com.mattermost.boardsv2"
	PlaybooksPluginID = "playbooks"
	CursorPluginID    = "com.mattermost.plugin-cursor"

	BoardsOrigin    = "plugin://" + BoardsPluginID
	PlaybooksOrigin = "plugin://" + PlaybooksPluginID
	CursorOrigin    = "plugin://" + CursorPluginID
)

// ErrToolOutsideProject is returned by a bound tool whose call would act
// outside the project.
var ErrToolOutsideProject = errors.New("this tool can only be used for this project's board and checklist")

// boardBoundTools act on one board and get board_id bound to the project's
// board.
var boardBoundTools = map[string]bool{
	"get_board":    true,
	"list_cards":   true,
	"search_cards": true,
	"create_card":  true,
	"list_views":   true,
	"get_fields":   true,
}

// boardCardTools act on a card. Their board_id, when the schema has one, is
// bound to the project's board.
var boardCardTools = map[string]bool{
	"get_card":          true,
	"update_card":       true,
	"get_card_comments": true,
	"add_card_comment":  true,
}

// playbookRunTools act on one checklist run and get run_id bound to the
// project's run.
var playbookRunTools = map[string]bool{
	"add_checklist_item":      true,
	"check_item":              true,
	"finish_run":              true,
	"update_run_status":       true,
	"get_run":                 true,
	"resolve_channel_context": true,
}

// cursorTools start or continue external Cursor work; they always ask.
var cursorTools = map[string]bool{
	"create_agent": true,
	"add_followup": true,
}

// cursorAgentTools act on one Cursor agent, named by agent_id, which must be
// one the project's run threads started.
var cursorAgentTools = map[string]bool{
	"add_followup":           true,
	"get_agent":              true,
	"get_agent_conversation": true,
}

// ErrNotInRunThread is returned when create_agent is called outside a run
// thread that has no Cursor agent yet.
var ErrNotInRunThread = errors.New("start the Cursor agent in its run thread: open one with start_cursor_thread first")

// BinderCallbacks record links the project agent creates.
type BinderCallbacks struct {
	// BoardCreated records the project's board channel and grants the bot
	// channel admin there. llmCtx is the turn that created it.
	BoardCreated func(ctx context.Context, llmCtx *llm.Context, project *store.Project, boardChannelID string) error
	// RunCreated records the project's checklist run. llmCtx is the turn that
	// created it.
	RunCreated func(ctx context.Context, llmCtx *llm.Context, project *store.Project, runID string) error
	// Current returns the project's current state, so once-only creates
	// re-check it at call time.
	Current func(channelID string) (*store.Project, error)
	// RunThread returns the run thread rooted at a post, or
	// store.ErrProjectThreadNotFound.
	RunThread func(channelID, rootPostID string) (*store.ProjectThread, error)
	// CursorAgentCreated records the Cursor agent a run thread started.
	CursorAgentCreated func(ctx context.Context, project *store.Project, rootPostID, agentID string) error
	// CursorAgents returns the Cursor agents the project's run threads
	// started.
	CursorAgents func(channelID string) ([]string, error)
	// CursorCreates guards create_agent per run thread across the binders
	// of every turn. Without it the guard covers only this binder.
	CursorCreates *OnceStates
}

// Binder wraps plugin MCP tools so every call is bound to one project. Tools
// it has no rule for are dropped.
type Binder struct {
	project                *store.Project
	interactiveUserPresent bool
	callbacks              BinderCallbacks

	mu            sync.Mutex
	boardCreate   onceState
	runCreate     onceState
	cursorCreates *OnceStates
}

// onceState tracks one once-only create. inFlight reserves it while the
// create runs without holding the binder's lock. pending holds the result of
// a create whose link failed, so a retry links it instead of creating again.
type onceState struct {
	inFlight bool
	done     bool
	pending  string
}

// OnceStates holds the once-only state of creates keyed by what they create
// for, such as a run thread.
type OnceStates struct {
	mu     sync.Mutex
	states map[string]*onceState
}

// NewOnceStates returns an empty OnceStates.
func NewOnceStates() *OnceStates {
	return &OnceStates{states: map[string]*onceState{}}
}

// claim reserves key's create. It returns the pending result of an earlier
// create whose record failed, and false while another create of key runs.
func (o *OnceStates) claim(key string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	state := o.states[key]
	if state == nil {
		state = &onceState{}
		o.states[key] = state
	}
	if state.inFlight {
		return "", false
	}
	state.inFlight = true
	return state.pending, true
}

// release ends key's create, keeping created as pending unless it was
// recorded.
func (o *OnceStates) release(key, created string, recorded bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if recorded || created == "" {
		delete(o.states, key)
		return
	}
	o.states[key] = &onceState{pending: created}
}

// NewBinder returns a binder for project.
func NewBinder(project *store.Project, interactiveUserPresent bool, callbacks BinderCallbacks) *Binder {
	cursorCreates := callbacks.CursorCreates
	if cursorCreates == nil {
		cursorCreates = NewOnceStates()
	}
	return &Binder{project: project, interactiveUserPresent: interactiveUserPresent, callbacks: callbacks, cursorCreates: cursorCreates}
}

// Bind returns the tools bound to the project, dropping every tool without a
// rule.
func (b *Binder) Bind(tools []llm.Tool) []llm.Tool {
	if b == nil || b.project == nil || !b.project.Live() {
		return nil
	}
	bound := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if t, ok := b.bind(tool); ok {
			bound = append(bound, t)
		}
	}
	return bound
}

// projectEmbeddedTools are the embedded Mattermost tools a project agent
// uses. The scoped session still limits them to the project's channels, and
// the writes to what its write grant covers.
var projectEmbeddedTools = []string{
	"read_channel", "read_post", "search_posts", "get_channel_info", "get_user", "get_user_by_username",
	"list_channel_bookmarks", "list_channel_artifacts", "get_channel_artifact",
	"add_reaction", "create_channel_bookmark", "update_channel_bookmark",
}

// ProjectEmbeddedTools is a project agent's embedded Mattermost allowlist.
func ProjectEmbeddedTools() []llm.EnabledMCPTool {
	tools := make([]llm.EnabledMCPTool, 0, len(projectEmbeddedTools))
	for _, name := range projectEmbeddedTools {
		tools = append(tools, llm.EnabledMCPTool{ServerOrigin: mcp.EmbeddedClientKey, ToolName: name})
	}
	return tools
}

// pluginToolName is the plugin's own name for an MCP tool. Catalogs prefix
// it with the server namespace, and plugins add their own prefix, as in
// boards__com_mattermost_boardsv2__create_board.
func pluginToolName(name string) string {
	if _, tool, found := strings.CutLast(name, llm.MCPToolNameSeparator); found {
		return tool
	}
	return name
}

// bind matches rules on the plugin's tool name; the bound tool keeps its
// namespaced name. The server origin, not the name prefix, picks the rules.
func (b *Binder) bind(tool llm.Tool) (llm.Tool, bool) {
	name := pluginToolName(tool.Name)
	switch llm.NormalizeMCPServerOrigin(tool.ServerOrigin) {
	case BoardsOrigin:
		return b.bindBoards(tool, name)
	case PlaybooksOrigin:
		return b.bindPlaybooks(tool, name)
	case CursorOrigin:
		return b.bindCursor(tool, name)
	default:
		return llm.Tool{}, false
	}
}

func (b *Binder) bindBoards(tool llm.Tool, name string) (llm.Tool, bool) {
	boardID := b.project.BoardChannelID
	switch {
	case boardBoundTools[name]:
		if boardID == "" {
			return llm.Tool{}, false
		}
		return tool.WithBoundParams(map[string]any{"board_id": boardID}), true
	case boardCardTools[name]:
		if boardID == "" {
			return llm.Tool{}, false
		}
		if schemaHasProperty(tool.Schema, "board_id") {
			return tool.WithBoundParams(map[string]any{"board_id": boardID}), true
		}
		return tool, true
	case name == "create_board":
		if boardID != "" {
			return llm.Tool{}, false
		}
		if schemaHasProperty(tool.Schema, "team_id") {
			tool = tool.WithBoundParams(map[string]any{"team_id": b.project.TeamID})
		}
		return b.onceOnly(tool, &b.boardCreate, func(c *store.Project) bool { return c.BoardChannelID == "" },
			func(ctx context.Context, llmCtx *llm.Context, result string) error {
				id := resultID(result, "board_id", "channel_id", "id")
				if id == "" {
					return errors.New("the created board did not report its channel")
				}
				if b.callbacks.BoardCreated == nil {
					return errors.New("board creation is not available")
				}
				return b.callbacks.BoardCreated(ctx, llmCtx, b.project, id)
			}), true
	default:
		return llm.Tool{}, false
	}
}

func (b *Binder) bindPlaybooks(tool llm.Tool, name string) (llm.Tool, bool) {
	runID := b.project.PlaybookRunID
	switch {
	case playbookRunTools[name]:
		if runID == "" {
			return llm.Tool{}, false
		}
		return tool.WithBoundParams(map[string]any{"run_id": runID}), true
	case name == "create_checklist":
		if runID != "" {
			return llm.Tool{}, false
		}
		tool = tool.WithBoundParams(map[string]any{"channel_id": b.project.ChannelID})
		return b.onceOnly(tool, &b.runCreate, func(c *store.Project) bool { return c.PlaybookRunID == "" },
			func(ctx context.Context, llmCtx *llm.Context, result string) error {
				id := resultID(result, "run_id", "id")
				if id == "" {
					return errors.New("the created checklist did not report its run")
				}
				if b.callbacks.RunCreated == nil {
					return errors.New("checklist creation is not available")
				}
				return b.callbacks.RunCreated(ctx, llmCtx, b.project, id)
			}), true
	default:
		return llm.Tool{}, false
	}
}

func (b *Binder) bindCursor(tool llm.Tool, name string) (llm.Tool, bool) {
	if !b.interactiveUserPresent {
		return llm.Tool{}, false
	}
	switch {
	case name == "create_agent":
		tool = b.cursorCreate(tool)
	case cursorAgentTools[name]:
		tool = b.cursorAgentBound(tool)
	case name == "list_environments":
	default:
		return llm.Tool{}, false
	}
	if cursorTools[name] {
		tool.AlwaysAsk = true
		tool.AutoExecute = false
	}
	return tool, true
}

// ErrCreateInProgress is returned while another call of the same create
// runs.
var ErrCreateInProgress = errors.New("this is already being created; wait for that call to finish")

// cursorCreate lets create_agent run only in a run thread that has no
// Cursor agent yet, one call at a time, and records the agent it starts on
// that thread. An agent started but not recorded is recorded by the next
// call instead of starting another.
func (b *Binder) cursorCreate(tool llm.Tool) llm.Tool {
	inner := tool.Resolver
	tool.Resolver = func(ctx context.Context, llmCtx *llm.Context, args llm.ToolArgumentGetter) (string, error) {
		if inner == nil || b.callbacks.RunThread == nil || b.callbacks.CursorAgentCreated == nil {
			return "", ErrToolOutsideProject
		}
		rootID := turnThreadRoot(llmCtx)
		if rootID == "" {
			return "", ErrNotInRunThread
		}
		key := b.project.ChannelID + "/" + rootID
		pending, ok := b.cursorCreates.claim(key)
		if !ok {
			return "", ErrCreateInProgress
		}
		agentID, recorded := pending, false
		defer func() { b.cursorCreates.release(key, agentID, recorded) }()

		// Read the thread after claiming, so an agent another call recorded
		// meanwhile is seen.
		thread, err := b.callbacks.RunThread(b.project.ChannelID, rootID)
		if errors.Is(err, store.ErrProjectThreadNotFound) {
			return "", ErrNotInRunThread
		}
		if err != nil {
			return "", fmt.Errorf("failed to check the run thread: %w", err)
		}
		if thread.Kind != store.ProjectThreadKindCursor {
			return "", ErrNotInRunThread
		}

		if pending != "" {
			if recordErr := b.callbacks.CursorAgentCreated(ctx, b.project, rootID, pending); recordErr != nil {
				if errors.Is(recordErr, store.ErrProjectLinkSet) {
					agentID = ""
				}
				return "", fmt.Errorf("the Cursor agent %s was started, but recording it on the run thread failed: %w", pending, recordErr)
			}
			recorded = true
			return fmt.Sprintf(`{"agent_id":%q}`, pending), nil
		}
		if thread.ExternalID != "" {
			return "", errors.New("this run thread already has a Cursor agent; use add_followup to continue it")
		}
		result, err := inner(ctx, llmCtx, args)
		if err != nil {
			return result, err
		}
		agentID = cursorAgentID(result)
		if agentID == "" {
			return result, errors.New("the Cursor agent was started, but its ID was not in the result")
		}
		if err := b.callbacks.CursorAgentCreated(ctx, b.project, rootID, agentID); err != nil {
			return result, fmt.Errorf("the Cursor agent was started, but recording it on the run thread failed: %w", err)
		}
		recorded = true
		return result, nil
	}
	return tool
}

// cursorAgentBound refuses calls whose agent_id isn't a Cursor agent the
// project's run threads started.
func (b *Binder) cursorAgentBound(tool llm.Tool) llm.Tool {
	inner := tool.Resolver
	tool.Resolver = func(ctx context.Context, llmCtx *llm.Context, args llm.ToolArgumentGetter) (string, error) {
		if inner == nil || b.callbacks.CursorAgents == nil {
			return "", ErrToolOutsideProject
		}
		var call struct {
			AgentID string `json:"agent_id"`
		}
		if err := args(&call); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		agents, err := b.callbacks.CursorAgents(b.project.ChannelID)
		if err != nil {
			return "", fmt.Errorf("failed to check the project's Cursor agents: %w", err)
		}
		if call.AgentID == "" || !slices.Contains(agents, call.AgentID) {
			return "", ErrToolOutsideProject
		}
		return inner(ctx, llmCtx, args)
	}
	return tool
}

// cursorAgentIDPattern matches a Cursor cloud agent ID in a text result.
var cursorAgentIDPattern = regexp.MustCompile(`\bbc-[A-Za-z0-9][A-Za-z0-9-]*`)

// cursorAgentID returns the agent ID in a create_agent result: the JSON
// agent_id (or id), else the first Cursor agent ID in the text.
func cursorAgentID(result string) string {
	if id := resultID(result, "agent_id", "id"); id != "" {
		return id
	}
	return cursorAgentIDPattern.FindString(result)
}

// turnThreadRoot is the root post of the thread llmCtx's turn runs in.
func turnThreadRoot(llmCtx *llm.Context) string {
	if llmCtx == nil || llmCtx.ToolCatalog.ScopedTurn == nil {
		return ""
	}
	return llmCtx.ToolCatalog.ScopedTurn.ThreadRootID
}

// onceOnly lets a create tool succeed at most once per project: once per
// binder, and only while the project, re-read at call time, still lacks the
// link. record runs after the create succeeded.
func (b *Binder) onceOnly(tool llm.Tool, state *onceState, missing func(*store.Project) bool, record func(ctx context.Context, llmCtx *llm.Context, result string) error) llm.Tool {
	inner := tool.Resolver
	tool.Resolver = func(ctx context.Context, llmCtx *llm.Context, args llm.ToolArgumentGetter) (string, error) {
		b.mu.Lock()
		if state.done || state.inFlight {
			b.mu.Unlock()
			return "", ErrToolOutsideProject
		}
		state.inFlight = true
		pending := state.pending
		b.mu.Unlock()
		created, linked := pending, false
		defer func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			state.inFlight = false
			if linked {
				state.done, state.pending = true, ""
			} else {
				state.pending = created
			}
		}()

		if pending != "" {
			if err := record(ctx, llmCtx, pending); err != nil {
				return "", fmt.Errorf("created, but failed to link it to the project: %w", err)
			}
			linked = true
			return pending, nil
		}
		if b.callbacks.Current == nil {
			return "", ErrToolOutsideProject
		}
		current, err := b.callbacks.Current(b.project.ChannelID)
		if err != nil {
			return "", fmt.Errorf("failed to check the project: %w", err)
		}
		if !current.Live() || !missing(current) {
			return "", ErrToolOutsideProject
		}
		if inner == nil {
			return "", errors.New("tool has no resolver")
		}
		result, err := inner(ctx, llmCtx, args)
		if err != nil {
			return result, err
		}
		created = result
		if err := record(ctx, llmCtx, result); err != nil {
			return result, fmt.Errorf("created, but failed to link it to the project: %w", err)
		}
		linked = true
		return result, nil
	}
	return tool
}

func schemaHasProperty(schema any, name string) bool {
	var m map[string]any
	switch s := schema.(type) {
	case map[string]any:
		m = s
	case nil:
		return false
	default:
		raw, err := json.Marshal(s)
		if err != nil || json.Unmarshal(raw, &m) != nil {
			return false
		}
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = props[name]
	return ok
}

// resultID returns the first of keys found in a JSON tool result, looking one
// level into nested objects too.
func resultID(result string, keys ...string) string {
	var m map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &m) != nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	for _, v := range m {
		if nested, ok := v.(map[string]any); ok {
			for _, key := range keys {
				if s, ok := nested[key].(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return ""
}
