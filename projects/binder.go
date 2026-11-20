// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
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

// cursorTools start or continue external Cursor work.
var cursorTools = map[string]bool{
	"create_agent": true,
	"add_followup": true,
}

// BinderCallbacks record links the project agent creates.
type BinderCallbacks struct {
	// BoardCreated records the project's board channel and grants the bot
	// channel admin there.
	BoardCreated func(ctx context.Context, project *store.Project, boardChannelID string) error
	// RunCreated records the project's checklist run.
	RunCreated func(ctx context.Context, project *store.Project, runID string) error
	// Current returns the project's current state, so once-only creates
	// re-check it at call time.
	Current func(channelID string) (*store.Project, error)
}

// Binder wraps plugin MCP tools so every call is bound to one project. Tools
// it has no rule for are dropped.
type Binder struct {
	project                *store.Project
	interactiveUserPresent bool
	callbacks              BinderCallbacks

	mu          sync.Mutex
	boardCreate onceState
	runCreate   onceState
}

// onceState tracks one once-only create. inFlight reserves it while the
// create runs without holding the binder's lock.
type onceState struct {
	inFlight bool
	done     bool
}

// NewBinder returns a binder for project.
func NewBinder(project *store.Project, interactiveUserPresent bool, callbacks BinderCallbacks) *Binder {
	return &Binder{project: project, interactiveUserPresent: interactiveUserPresent, callbacks: callbacks}
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

func (b *Binder) bind(tool llm.Tool) (llm.Tool, bool) {
	switch llm.NormalizeMCPServerOrigin(tool.ServerOrigin) {
	case BoardsOrigin:
		return b.bindBoards(tool)
	case PlaybooksOrigin:
		return b.bindPlaybooks(tool)
	case CursorOrigin:
		return b.bindCursor(tool)
	default:
		return llm.Tool{}, false
	}
}

func (b *Binder) bindBoards(tool llm.Tool) (llm.Tool, bool) {
	boardID := b.project.BoardChannelID
	switch {
	case boardBoundTools[tool.Name]:
		if boardID == "" {
			return llm.Tool{}, false
		}
		return tool.WithBoundParams(map[string]any{"board_id": boardID}), true
	case boardCardTools[tool.Name]:
		if boardID == "" {
			return llm.Tool{}, false
		}
		if schemaHasProperty(tool.Schema, "board_id") {
			return tool.WithBoundParams(map[string]any{"board_id": boardID}), true
		}
		return tool, true
	case tool.Name == "create_board":
		if boardID != "" {
			return llm.Tool{}, false
		}
		if schemaHasProperty(tool.Schema, "team_id") {
			tool = tool.WithBoundParams(map[string]any{"team_id": b.project.TeamID})
		}
		return b.onceOnly(tool, &b.boardCreate, func(c *store.Project) bool { return c.BoardChannelID == "" },
			func(ctx context.Context, result string) error {
				id := resultID(result, "board_id", "channel_id", "id")
				if id == "" {
					return errors.New("the created board did not report its channel")
				}
				if b.callbacks.BoardCreated == nil {
					return errors.New("board creation is not available")
				}
				return b.callbacks.BoardCreated(ctx, b.project, id)
			}), true
	default:
		return llm.Tool{}, false
	}
}

func (b *Binder) bindPlaybooks(tool llm.Tool) (llm.Tool, bool) {
	runID := b.project.PlaybookRunID
	switch {
	case playbookRunTools[tool.Name]:
		if runID == "" {
			return llm.Tool{}, false
		}
		return tool.WithBoundParams(map[string]any{"run_id": runID}), true
	case tool.Name == "create_checklist":
		if runID != "" {
			return llm.Tool{}, false
		}
		tool = tool.WithBoundParams(map[string]any{"channel_id": b.project.ChannelID})
		return b.onceOnly(tool, &b.runCreate, func(c *store.Project) bool { return c.PlaybookRunID == "" },
			func(ctx context.Context, result string) error {
				id := resultID(result, "run_id", "id")
				if id == "" {
					return errors.New("the created checklist did not report its run")
				}
				if b.callbacks.RunCreated == nil {
					return errors.New("checklist creation is not available")
				}
				return b.callbacks.RunCreated(ctx, b.project, id)
			}), true
	default:
		return llm.Tool{}, false
	}
}

func (b *Binder) bindCursor(tool llm.Tool) (llm.Tool, bool) {
	if !cursorTools[tool.Name] || !b.interactiveUserPresent {
		return llm.Tool{}, false
	}
	tool.AlwaysAsk = true
	tool.AutoExecute = false
	return tool, true
}

// onceOnly lets a create tool succeed at most once per project: once per
// binder, and only while the project, re-read at call time, still lacks the
// link. record runs after the create succeeded.
func (b *Binder) onceOnly(tool llm.Tool, state *onceState, missing func(*store.Project) bool, record func(ctx context.Context, result string) error) llm.Tool {
	inner := tool.Resolver
	tool.Resolver = func(ctx context.Context, llmCtx *llm.Context, args llm.ToolArgumentGetter) (string, error) {
		b.mu.Lock()
		if state.done || state.inFlight {
			b.mu.Unlock()
			return "", ErrToolOutsideProject
		}
		state.inFlight = true
		b.mu.Unlock()
		created := false
		defer func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			state.inFlight = false
			state.done = state.done || created
		}()

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
		created = true
		if err := record(ctx, result); err != nil {
			return result, fmt.Errorf("created, but failed to link it to the project: %w", err)
		}
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
