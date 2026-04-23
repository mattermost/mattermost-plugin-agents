// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcptool

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestSearchPostsOutput_JSONStableKeys(t *testing.T) {
	o := SearchPostsOutput{
		Query: "hello",
		SemanticResults: []SearchPostResult{
			{
				Post:        &model.Post{Id: "p1", Message: "m"},
				ChannelName: "c",
				Username:    "u",
				Score:       0.5,
				Source:      "semantic",
			},
		},
	}
	b, err := json.Marshal(o)
	require.NoError(t, err)
	require.Contains(t, string(b), `"post"`)
	require.Contains(t, string(b), `"channel_name"`)

	var back SearchPostsOutput
	require.NoError(t, json.Unmarshal(b, &back))
	require.Len(t, back.SemanticResults, 1)
	require.Equal(t, "p1", back.SemanticResults[0].Post.Id)
}
