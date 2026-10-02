// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnnotateHeldAnswersNamesPrivateSourcesForRequester(t *testing.T) {
	dest := &model.Channel{Id: "dest", Type: model.ChannelTypeOpen, TeamId: "team-a", Name: "help-desk", DisplayName: "Help Desk"}
	secret := &model.Channel{Id: "secret", Type: model.ChannelTypePrivate, TeamId: "team-a", Name: "procurement-restricted", DisplayName: "Procurement Restricted"}
	llmCtx := &llm.Context{
		Channel:                dest,
		DestinationHasNoGuests: true,
		Parameters: map[string]any{
			sourceChannelsParam: []*model.Channel{secret},
		},
	}
	turns := []toolrunner.ToolTurn{{
		HeldAnswer: "The switch was agreed in Procurement Restricted.",
	}}

	annotateHeldAnswers(turns, llmCtx)

	require.NotEmpty(t, turns[0].HeldAnnotations)
	assert.True(t, turns[0].HeldAnnotations[0].Private)
	assert.Equal(t, "Procurement Restricted", turns[0].HeldAnnotations[0].ChannelName)
	require.NotEmpty(t, turns[0].HeldSources)
	assert.True(t, turns[0].HeldSources[0].Private)
}

func TestSanitizePublishedAnswerDropsPrivateChannelNames(t *testing.T) {
	got := sanitizePublishedAnswer(
		"Agreed in Procurement Restricted (~procurement-restricted): price down 12%.",
		[]conversation.SourceChannel{{
			Name:        "procurement-restricted",
			DisplayName: "Procurement Restricted",
			Private:     true,
		}},
	)
	assert.NotContains(t, got, "Procurement")
	assert.NotContains(t, got, "procurement-restricted")
	assert.Contains(t, got, "price down 12%")
}
