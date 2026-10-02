// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	cases := []struct {
		name     string
		text     string
		sources  []conversation.SourceChannel
		want     []string
		wantGone []string
	}{
		{
			name: "drops private display name and tilde handle",
			text: "Agreed in Procurement Restricted (~procurement-restricted): price down 12%.",
			sources: []conversation.SourceChannel{{
				Name:        "procurement-restricted",
				DisplayName: "Procurement Restricted",
				Private:     true,
			}},
			want:     []string{"price down 12%"},
			wantGone: []string{"Procurement", "procurement-restricted"},
		},
		{
			name: "keeps longer words that only share a prefix",
			text: "The development plan in Dev (~dev) is ready.",
			sources: []conversation.SourceChannel{{
				Name:        "dev",
				DisplayName: "Dev",
				Private:     true,
			}},
			want:     []string{"development"},
			wantGone: []string{"~dev"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizePublishedAnswer(tc.text, tc.sources)
			for _, keep := range tc.want {
				assert.Contains(t, got, keep)
			}
			for _, gone := range tc.wantGone {
				assert.NotContains(t, got, gone)
			}
		})
	}
}

func TestTurnMatchesClickedTools(t *testing.T) {
	clicked := map[string]struct{}{"tool-a": {}}
	cases := []struct {
		name    string
		blocks  []conversation.ContentBlock
		clicked map[string]struct{}
		want    bool
	}{
		{
			name:    "empty clicked set never matches",
			blocks:  []conversation.ContentBlock{{Type: conversation.BlockTypeToolUse, ID: "tool-a"}},
			clicked: nil,
			want:    false,
		},
		{
			name:    "matching tool_use",
			blocks:  []conversation.ContentBlock{{Type: conversation.BlockTypeToolUse, ID: "tool-a"}},
			clicked: clicked,
			want:    true,
		},
		{
			name:    "matching tool_result",
			blocks:  []conversation.ContentBlock{{Type: conversation.BlockTypeToolResult, ToolUseID: "tool-a"}},
			clicked: clicked,
			want:    true,
		},
		{
			name:    "other round",
			blocks:  []conversation.ContentBlock{{Type: conversation.BlockTypeToolUse, ID: "tool-b"}},
			clicked: clicked,
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, turnMatchesClickedTools(tc.blocks, tc.clicked))
		})
	}
}

func TestPublishHeldChannelAnswerScopesToClickedRound(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	clickedID := "tool-a"
	otherID := "tool-b"

	clickedBlocks := []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolUse, ID: clickedID, Name: "read_channel"},
		{
			Type:          conversation.BlockTypeText,
			Text:          "clicked draft",
			RequesterOnly: true,
		},
	}
	otherBlocks := []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolUse, ID: otherID, Name: "read_channel"},
		{
			Type:          conversation.BlockTypeText,
			Text:          "other draft",
			RequesterOnly: true,
		},
	}
	clickedJSON, err := json.Marshal(clickedBlocks)
	require.NoError(t, err)
	otherJSON, err := json.Marshal(otherBlocks)
	require.NoError(t, err)

	clickedTurn := store.Turn{ID: "turn-a", ConversationID: conv.ID, Role: "assistant", Content: clickedJSON, Sequence: 1}
	otherTurn := store.Turn{ID: "turn-b", ConversationID: conv.ID, Role: "assistant", Content: otherJSON, Sequence: 2}
	require.NoError(t, convStore.CreateTurn(&clickedTurn))
	require.NoError(t, convStore.CreateTurn(&otherTurn))

	mmClient := mocks.NewMockClient(t)
	mmClient.On("UpdatePost", mock.Anything).Return(nil).Once()

	c := &Conversations{
		mmClient:    mmClient,
		convService: conversation.NewService(convStore, nil, nil, nil),
	}

	decoded := [][]conversation.ContentBlock{clickedBlocks, otherBlocks}
	turns := []store.Turn{clickedTurn, otherTurn}
	post := &model.Post{Id: "post-a"}

	require.True(t, c.publishHeldChannelAnswer(post, turns, decoded, map[string]struct{}{clickedID: {}}))
	assert.Equal(t, "clicked draft", post.Message)

	updated, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, updated, 2)

	var published, held []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(updated[0].Content, &published))
	require.NoError(t, json.Unmarshal(updated[1].Content, &held))
	require.False(t, published[1].RequesterOnly)
	assert.Equal(t, "clicked draft", published[1].Text)
	require.True(t, held[1].RequesterOnly)
	assert.Equal(t, "other draft", held[1].Text)
}
