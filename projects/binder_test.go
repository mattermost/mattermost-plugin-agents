// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// recordingTool returns a tool whose resolver stores the arguments it was
// called with and returns result.
func recordingTool(origin, name string, props []string, result string, got *map[string]any) llm.Tool {
	properties := map[string]any{}
	for _, p := range props {
		properties[p] = map[string]any{"type": "string"}
	}
	return llm.Tool{
		Name:         name,
		ServerOrigin: origin,
		Schema:       map[string]any{"type": "object", "properties": properties},
		Resolver: func(_ context.Context, _ *llm.Context, get llm.ToolArgumentGetter) (string, error) {
			args := map[string]any{}
			if err := get(&args); err != nil {
				return "", err
			}
			*got = args
			return result, nil
		},
	}
}

func callTool(t *testing.T, tool llm.Tool, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return tool.Resolver(context.Background(), &llm.Context{}, func(v any) error { return json.Unmarshal(raw, v) })
}

func TestBinderRules(t *testing.T) {
	withBoard := &store.Project{ChannelID: "P", TeamID: "T", State: store.ProjectStateActive, BoardChannelID: "B", PlaybookRunID: "R"}
	bare := &store.Project{ChannelID: "P", TeamID: "T", State: store.ProjectStateActive}

	tests := []struct {
		name        string
		project     *store.Project
		interactive bool
		origin      string
		tool        string
		props       []string
		modelArgs   map[string]any
		wantKept    bool
		wantArgs    map[string]any
		wantAsk     bool
	}{
		{name: "board read binds board_id", project: withBoard, origin: BoardsOrigin, tool: "list_cards", props: []string{"board_id"},
			modelArgs: map[string]any{"board_id": "OTHER"}, wantKept: true, wantArgs: map[string]any{"board_id": "B"}},
		{name: "card tool with board_id gets it bound", project: withBoard, origin: BoardsOrigin, tool: "update_card", props: []string{"board_id", "card_id"},
			modelArgs: map[string]any{"board_id": "OTHER", "card_id": "C"}, wantKept: true, wantArgs: map[string]any{"board_id": "B", "card_id": "C"}},
		{name: "card tool without board_id is not given one", project: withBoard, origin: BoardsOrigin, tool: "get_card", props: []string{"card_id"},
			modelArgs: map[string]any{"card_id": "C"}, wantKept: true, wantArgs: map[string]any{"card_id": "C"}},
		{name: "board tools dropped without a board", project: bare, origin: BoardsOrigin, tool: "list_cards", props: []string{"board_id"}},
		{name: "delete_card dropped", project: withBoard, origin: BoardsOrigin, tool: "delete_card", props: []string{"card_id"}},
		{name: "list_boards dropped", project: withBoard, origin: BoardsOrigin, tool: "list_boards"},
		{name: "manage_view dropped", project: withBoard, origin: BoardsOrigin, tool: "manage_view"},
		{name: "manage_fields dropped", project: withBoard, origin: BoardsOrigin, tool: "manage_fields"},
		{name: "unknown boards tool dropped", project: withBoard, origin: BoardsOrigin, tool: "export_everything"},
		{name: "create_board dropped once a board exists", project: withBoard, origin: BoardsOrigin, tool: "create_board"},
		{name: "run tool binds run_id", project: withBoard, origin: PlaybooksOrigin, tool: "check_item", props: []string{"run_id", "item"},
			modelArgs: map[string]any{"run_id": "OTHER", "item": "1"}, wantKept: true, wantArgs: map[string]any{"run_id": "R", "item": "1"}},
		{name: "run tools dropped without a run", project: bare, origin: PlaybooksOrigin, tool: "get_run", props: []string{"run_id"}},
		{name: "create_checklist dropped once a run exists", project: withBoard, origin: PlaybooksOrigin, tool: "create_checklist"},
		{name: "unknown playbooks tool dropped", project: withBoard, origin: PlaybooksOrigin, tool: "list_playbooks"},
		{name: "cursor create_agent always asks", project: withBoard, interactive: true, origin: CursorOrigin, tool: "create_agent", wantKept: true, wantAsk: true},
		{name: "cursor add_followup always asks", project: withBoard, interactive: true, origin: CursorOrigin, tool: "add_followup", wantKept: true, wantAsk: true},
		{name: "cursor dropped when nobody is present", project: withBoard, origin: CursorOrigin, tool: "create_agent"},
		{name: "unknown cursor tool dropped", project: withBoard, interactive: true, origin: CursorOrigin, tool: "delete_agent"},
		{name: "other origins dropped", project: withBoard, interactive: true, origin: "plugin://other", tool: "list_cards"},
		{name: "ended project binds nothing", project: &store.Project{ChannelID: "P", State: store.ProjectStateEnded, BoardChannelID: "B"}, origin: BoardsOrigin, tool: "list_cards", props: []string{"board_id"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			tool := recordingTool(tc.origin, tc.tool, tc.props, "ok", &got)
			if tc.wantAsk {
				tool.AutoExecute = true
			}
			bound := NewBinder(tc.project, tc.interactive, BinderCallbacks{}).Bind([]llm.Tool{tool})
			if !tc.wantKept {
				assert.Empty(t, bound)
				return
			}
			require.Len(t, bound, 1)
			assert.Equal(t, tc.wantAsk, bound[0].AlwaysAsk)
			if tc.wantAsk {
				assert.False(t, bound[0].AutoExecute)
			}
			if tc.modelArgs != nil {
				_, err := callTool(t, bound[0], tc.modelArgs)
				require.NoError(t, err)
				assert.Equal(t, tc.wantArgs, got)
			}
		})
	}
}

func TestBinderCreateOnce(t *testing.T) {
	tests := []struct {
		name      string
		origin    string
		tool      string
		props     []string
		result    string
		modelArgs map[string]any
		wantArgs  map[string]any
		wantLink  string
	}{
		{name: "create_board records the board", origin: BoardsOrigin, tool: "create_board", props: []string{"team_id", "title"},
			result: `{"board":{"channel_id":"NEWB"}}`, modelArgs: map[string]any{"title": "x", "team_id": "EVIL"}, wantArgs: map[string]any{"team_id": "T", "title": "x"}, wantLink: "NEWB"},
		{name: "create_checklist binds the channel and records the run", origin: PlaybooksOrigin, tool: "create_checklist", props: []string{"channel_id", "title"},
			result: `{"run_id":"NEWR"}`, modelArgs: map[string]any{"title": "x", "channel_id": "EVIL"}, wantArgs: map[string]any{"channel_id": "P", "title": "x"}, wantLink: "NEWR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current := &store.Project{ChannelID: "P", TeamID: "T", State: store.ProjectStateActive}
			var linked string
			callbacks := BinderCallbacks{
				Current: func(string) (*store.Project, error) { return current, nil },
				BoardCreated: func(_ context.Context, _ *store.Project, id string) error {
					linked = id
					current.BoardChannelID = id
					return nil
				},
				RunCreated: func(_ context.Context, _ *store.Project, id string) error {
					linked = id
					current.PlaybookRunID = id
					return nil
				},
			}
			var got map[string]any
			snapshot := *current
			bound := NewBinder(&snapshot, false, callbacks).Bind([]llm.Tool{recordingTool(tc.origin, tc.tool, tc.props, tc.result, &got)})
			require.Len(t, bound, 1)

			_, err := callTool(t, bound[0], tc.modelArgs)
			require.NoError(t, err)
			assert.Equal(t, tc.wantArgs, got)
			assert.Equal(t, tc.wantLink, linked)

			got = nil
			_, err = callTool(t, bound[0], map[string]any{"title": "again"})
			require.ErrorIs(t, err, ErrToolOutsideProject)
			assert.Nil(t, got, "a second create must not reach the plugin")

			fresh := NewBinder(&snapshot, false, callbacks).Bind([]llm.Tool{recordingTool(tc.origin, tc.tool, tc.props, tc.result, &got)})
			require.Len(t, fresh, 1)
			_, err = callTool(t, fresh[0], map[string]any{"title": "again"})
			require.ErrorIs(t, err, ErrToolOutsideProject, "a stale binder re-checks the project before creating")
			assert.Nil(t, got)
		})
	}
}

func TestOnceOnlyCreateDoesNotHoldTheLockAcrossTheCall(t *testing.T) {
	tests := []struct {
		name      string
		firstErr  error
		wantRetry bool
	}{
		{name: "a finished create stays done"},
		{name: "a failed create can be retried", firstErr: errors.New("plugin down"), wantRetry: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current := &store.Project{ChannelID: "P", TeamID: "T", State: store.ProjectStateActive}
			callbacks := BinderCallbacks{
				Current:      func(string) (*store.Project, error) { return current, nil },
				BoardCreated: func(context.Context, *store.Project, string) error { return nil },
			}
			started, release := make(chan struct{}), make(chan struct{})
			calls := 0
			tool := llm.Tool{
				Name:         "create_board",
				ServerOrigin: BoardsOrigin,
				Schema:       map[string]any{"type": "object", "properties": map[string]any{"team_id": map[string]any{"type": "string"}}},
				Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
					calls++
					if calls == 1 {
						close(started)
						<-release
						return `{"board":{"channel_id":"NEWB"}}`, tc.firstErr
					}
					return `{"board":{"channel_id":"NEWB"}}`, nil
				},
			}
			bound := NewBinder(current, false, callbacks).Bind([]llm.Tool{tool})
			require.Len(t, bound, 1)

			call := func(done chan<- error) {
				_, err := bound[0].Resolver(context.Background(), &llm.Context{}, func(any) error { return nil })
				done <- err
			}
			firstDone := make(chan error, 1)
			go call(firstDone)
			<-started

			concurrent := make(chan error, 1)
			go call(concurrent)
			select {
			case err := <-concurrent:
				assert.ErrorIs(t, err, ErrToolOutsideProject, "a create already in flight refuses a second one")
			case <-time.After(5 * time.Second):
				assert.Fail(t, "a second call blocked behind the in-flight create")
			}
			close(release)
			if tc.firstErr == nil {
				assert.NoError(t, <-firstDone)
			} else {
				assert.ErrorIs(t, <-firstDone, tc.firstErr)
			}

			_, err := callTool(t, bound[0], map[string]any{})
			if tc.wantRetry {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrToolOutsideProject)
			}
		})
	}
}

func TestBinderMatchesNamespacedCatalogNames(t *testing.T) {
	project := &store.Project{ChannelID: "P", TeamID: "T", State: store.ProjectStateActive, BoardChannelID: "B", PlaybookRunID: "R"}
	tests := []struct {
		origin, slug, bare string
		wantArgs           map[string]any
	}{
		{origin: BoardsOrigin, slug: "boards", bare: "list_cards", wantArgs: map[string]any{"board_id": "B"}},
		{origin: PlaybooksOrigin, slug: "playbooks", bare: "check_item", wantArgs: map[string]any{"run_id": "R"}},
		{origin: CursorOrigin, slug: "cursor", bare: "create_agent", wantArgs: map[string]any{}},
	}
	for _, tc := range tests {
		t.Run(tc.bare, func(t *testing.T) {
			name := llm.NamespaceMCPToolName(tc.slug, tc.bare)
			var props []string
			for key := range tc.wantArgs {
				props = append(props, key)
			}
			var got map[string]any
			tool := recordingTool(tc.origin, name, props, "ok", &got)

			bound := NewBinder(project, true, BinderCallbacks{}).Bind([]llm.Tool{tool, recordingTool(tc.origin, llm.NamespaceMCPToolName(tc.slug, "delete_everything"), nil, "ok", &got)})
			require.Len(t, bound, 1, "the known tool is kept and the unknown one dropped")
			assert.Equal(t, name, bound[0].Name, "the bound tool keeps its catalog name")
			_, err := callTool(t, bound[0], map[string]any{})
			require.NoError(t, err)
			assert.Equal(t, tc.wantArgs, got)
		})
	}
}

func TestOnceOnlyCreateRetriesTheLinkWithoutCreatingAgain(t *testing.T) {
	current := &store.Project{ChannelID: "P", TeamID: "T", State: store.ProjectStateActive}
	linkErr := errors.New("database down")
	var linked []string
	callbacks := BinderCallbacks{
		Current: func(string) (*store.Project, error) { return current, nil },
		BoardCreated: func(_ context.Context, _ *store.Project, id string) error {
			if linkErr != nil {
				return linkErr
			}
			linked = append(linked, id)
			return nil
		},
	}
	creates := 0
	tool := llm.Tool{
		Name:         llm.NamespaceMCPToolName("boards", "create_board"),
		ServerOrigin: BoardsOrigin,
		Schema:       map[string]any{"type": "object", "properties": map[string]any{}},
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			creates++
			return `{"board":{"channel_id":"NEWB"}}`, nil
		},
	}
	bound := NewBinder(current, false, callbacks).Bind([]llm.Tool{tool})
	require.Len(t, bound, 1)

	_, err := callTool(t, bound[0], map[string]any{})
	require.ErrorIs(t, err, linkErr)

	linkErr = nil
	_, err = callTool(t, bound[0], map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, creates, "the retry links the board already created")
	assert.Equal(t, []string{"NEWB"}, linked)

	_, err = callTool(t, bound[0], map[string]any{})
	require.ErrorIs(t, err, ErrToolOutsideProject)
	assert.Equal(t, 1, creates)
}
