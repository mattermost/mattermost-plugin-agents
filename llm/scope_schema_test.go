// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestScopeTaggedParams(t *testing.T) {
	teamProp := &jsonschema.Schema{Type: "string"}
	teamProp.Extra = map[string]any{MattermostScopeSchemaExtraKey: MattermostScopeTagTeamID}
	chProp := &jsonschema.Schema{Type: "string"}
	chProp.Extra = map[string]any{MattermostScopeSchemaExtraKey: MattermostScopeTagChannelID}
	root := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"team_id":    teamProp,
			"channel_id": chProp,
			"query":      {Type: "string"},
		},
	}

	team, ch := ScopeTaggedParams(root)
	require.ElementsMatch(t, []string{"team_id"}, team)
	require.ElementsMatch(t, []string{"channel_id"}, ch)
}

func TestWithConstrainedParams_AddsEnumAndRequired(t *testing.T) {
	root := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"channel_id": {Type: "string"},
		},
		Required: []string{"query"},
	}
	tool := Tool{
		Name:   "search_posts",
		Schema: root,
		Resolver: func(_ *Context, _ ToolArgumentGetter) (string, error) {
			return "", nil
		},
	}
	ids := []string{model.NewId(), model.NewId()}
	out := tool.WithConstrainedParams(map[string][]string{"channel_id": ids})

	schema := out.Schema.(*jsonschema.Schema)
	require.Contains(t, schema.Required, "channel_id")
	require.Contains(t, schema.Required, "query")
	ch := schema.Properties["channel_id"]
	require.Len(t, ch.Enum, 2)
	require.Equal(t, ids[0], ch.Enum[0].(string))
	require.Equal(t, ids[1], ch.Enum[1].(string))
}

func TestApplyMattermostAccessScope_BindsTeamID(t *testing.T) {
	teamProp := &jsonschema.Schema{Type: "string"}
	teamProp.Extra = map[string]any{MattermostScopeSchemaExtraKey: MattermostScopeTagTeamID}
	root := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"team_id": teamProp,
			"q":       {Type: "string"},
		},
	}
	store := NewToolStore(nil, false)
	store.AddTools([]Tool{{
		Name:   "x",
		Schema: root,
		Resolver: func(_ *Context, _ ToolArgumentGetter) (string, error) {
			return "ok", nil
		},
	}})
	teamID := model.NewId()
	scope := &MattermostAccessScope{TeamID: teamID}
	ApplyMattermostAccessScope(store, scope)

	tool := store.GetTool("x")
	require.NotNil(t, tool)
	schema := tool.Schema.(*jsonschema.Schema)
	_, hasTeam := schema.Properties["team_id"]
	require.False(t, hasTeam, "team_id should be bound and removed from LLM schema")
}

func TestApplyMattermostAccessScope_MultiChannelEnum(t *testing.T) {
	chProp := &jsonschema.Schema{Type: "string"}
	chProp.Extra = map[string]any{MattermostScopeSchemaExtraKey: MattermostScopeTagChannelID}
	root := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"channel_id": chProp,
		},
	}
	store := NewToolStore(nil, false)
	store.AddTools([]Tool{{
		Name:   "search_posts",
		Schema: root,
		Resolver: func(_ *Context, _ ToolArgumentGetter) (string, error) {
			return "ok", nil
		},
	}})
	id1, id2 := model.NewId(), model.NewId()
	scope := &MattermostAccessScope{TeamID: model.NewId(), AllowedChannelIDs: []string{id1, id2}}
	ApplyMattermostAccessScope(store, scope)

	tool := store.GetTool("search_posts")
	schema := tool.Schema.(*jsonschema.Schema)
	require.Contains(t, schema.Required, "channel_id")
	require.Len(t, schema.Properties["channel_id"].Enum, 2)
}

func TestValidateMattermostAccessScopeArgs(t *testing.T) {
	teamProp := &jsonschema.Schema{Type: "string"}
	teamProp.Extra = map[string]any{MattermostScopeSchemaExtraKey: MattermostScopeTagTeamID}
	channelProp := &jsonschema.Schema{Type: "string"}
	channelProp.Extra = map[string]any{MattermostScopeSchemaExtraKey: MattermostScopeTagChannelID}
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"team_id":    teamProp,
			"channel_id": channelProp,
		},
		Required: []string{"channel_id"},
	}

	teamID := model.NewId()
	allowedChannelID := model.NewId()
	scope := &MattermostAccessScope{
		TeamID:            teamID,
		AllowedChannelIDs: []string{allowedChannelID},
	}

	testCases := []struct {
		name          string
		rawArgs       map[string]any
		errorContains string
	}{
		{
			name: "matching scoped values pass",
			rawArgs: map[string]any{
				"team_id":    teamID,
				"channel_id": allowedChannelID,
			},
		},
		{
			name: "team mismatch fails",
			rawArgs: map[string]any{
				"team_id":    model.NewId(),
				"channel_id": allowedChannelID,
			},
			errorContains: `field "team_id" value`,
		},
		{
			name: "channel mismatch fails",
			rawArgs: map[string]any{
				"channel_id": model.NewId(),
			},
			errorContains: `field "channel_id" value`,
		},
		{
			name: "non string scoped value fails",
			rawArgs: map[string]any{
				"channel_id": 123,
			},
			errorContains: `field "channel_id" must be a string`,
		},
		{
			name: "missing required scoped param fails",
			rawArgs: map[string]any{
				"team_id": teamID,
			},
			errorContains: `field "channel_id" is required by the execution scope`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMattermostAccessScopeArgs(schema, scope, tc.rawArgs)
			if tc.errorContains == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errorContains)
		})
	}
}

func TestResolveTool_BoundParamsOverrideUserInput(t *testing.T) {
	teamProp := &jsonschema.Schema{Type: "string"}
	teamProp.Extra = map[string]any{MattermostScopeSchemaExtraKey: MattermostScopeTagTeamID}
	root := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"team_id": teamProp,
			"q":       {Type: "string"},
		},
	}

	type argsStruct struct {
		TeamID string `json:"team_id"`
		Query  string `json:"q"`
	}

	store := NewToolStore(nil, false)
	store.AddTools([]Tool{{
		Name:   "search_posts",
		Schema: root,
		Resolver: func(_ *Context, argsGetter ToolArgumentGetter) (string, error) {
			var args argsStruct
			if err := argsGetter(&args); err != nil {
				return "", err
			}
			return args.TeamID + ":" + args.Query, nil
		},
	}})

	scopeTeamID := model.NewId()
	scope := &MattermostAccessScope{TeamID: scopeTeamID}
	ApplyMattermostAccessScope(store, scope)

	result, err := store.ResolveTool("search_posts", func(args any) error {
		return json.Unmarshal([]byte(`{"team_id":"`+model.NewId()+`","q":"hello"}`), args)
	}, &Context{})
	require.NoError(t, err)
	require.Equal(t, scopeTeamID+":hello", result)
}
