// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type inferredScopedArgs struct {
	TeamID    string `json:"team_id" scope:"true" jsonschema:"Scoped team ID"`
	ChannelID string `json:"channel_id" scope:"true" jsonschema:"Scoped channel ID"`
	Query     string `json:"query" jsonschema:"Search query"`
}

type explicitScopedOverrideArgs struct {
	ChannelIDs string `json:"channel_ids" scope:"channel_id" jsonschema:"Scoped channel IDs"`
}

type invalidMarkerScopedArgs struct {
	ScopedQuery string `json:"query" scope:"true" jsonschema:"Unsupported inferred scope"`
}

type invalidExplicitScopedArgs struct {
	TeamID string `json:"team_id" scope:"workspace_id" jsonschema:"Invalid explicit scope"`
}

func TestAnnotateMattermostScopeTags(t *testing.T) {
	base := NewJSONSchemaForAccessMode[inferredScopedArgs]("remote")
	annotated := AnnotateMattermostScopeTags[inferredScopedArgs](base)

	require.NotNil(t, base)
	require.NotNil(t, annotated)
	require.Nil(t, base.Properties["team_id"].Extra)
	require.Nil(t, base.Properties["channel_id"].Extra)

	teamParams, channelParams := llm.ScopeTaggedParams(annotated)
	require.ElementsMatch(t, []string{"team_id"}, teamParams)
	require.ElementsMatch(t, []string{"channel_id"}, channelParams)
}

func TestAnnotateMattermostScopeTags_AllowsExplicitOverride(t *testing.T) {
	annotated := AnnotateMattermostScopeTags[explicitScopedOverrideArgs](NewJSONSchemaForAccessMode[explicitScopedOverrideArgs]("remote"))

	teamParams, channelParams := llm.ScopeTaggedParams(annotated)
	require.Empty(t, teamParams)
	require.ElementsMatch(t, []string{"channel_ids"}, channelParams)
}

func TestAnnotateMattermostScopeTags_PanicsOnInvalidScopeTags(t *testing.T) {
	t.Run("marker cannot infer scope kind", func(t *testing.T) {
		assert.PanicsWithValue(t, `unsupported inferred mattermost scope for json field "query"`, func() {
			AnnotateMattermostScopeTags[invalidMarkerScopedArgs](NewJSONSchemaForAccessMode[invalidMarkerScopedArgs]("remote"))
		})
	})

	t.Run("explicit tag must be supported scope kind", func(t *testing.T) {
		assert.PanicsWithValue(t, `unsupported mattermost scope tag "workspace_id" on field "TeamID"`, func() {
			AnnotateMattermostScopeTags[invalidExplicitScopedArgs](NewJSONSchemaForAccessMode[invalidExplicitScopedArgs]("remote"))
		})
	})
}
