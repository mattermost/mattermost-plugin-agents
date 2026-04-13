// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/require"
)

func TestAnnotateMattermostScopeTags(t *testing.T) {
	base := NewJSONSchemaForAccessMode[ScopedArgs]("remote")
	annotated := AnnotateMattermostScopeTags[ScopedArgs](base)

	require.NotNil(t, base)
	require.NotNil(t, annotated)
	require.Nil(t, base.Properties["team_id"].Extra)
	require.Nil(t, base.Properties["channel_id"].Extra)

	teamParams, channelParams := llm.ScopeTaggedParams(annotated)
	require.ElementsMatch(t, []string{"team_id"}, teamParams)
	require.ElementsMatch(t, []string{"channel_id"}, channelParams)
}
