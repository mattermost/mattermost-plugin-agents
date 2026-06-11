// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestHandleToolCallAnswersUserQuestion covers the AskUserQuestion answer
// round-trip: the user's selection becomes the tool result, answer results are
// terminal (no share stage) even in channels, skips record a decline, and an
// invalid answer fails the request without consuming the pending question.
func TestHandleToolCallAnswersUserQuestion(t *testing.T) {
	questionInput := json.RawMessage(`{
		"question": "Which channel should I post in?",
		"options": [{"label": "UX Design"}, {"label": "Design team"}]
	}`)

	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	openChannel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	cases := []struct {
		name          string
		channel       *model.Channel
		acceptedIDs   []string
		answers       map[string][]string
		wantErr       error
		wantStatus    string
		wantResult    string
		wantShared    bool
		wantFollowUp  bool
		wantResultErr bool
	}{
		{
			name:         "answered in DM streams follow-up",
			channel:      dmChannel,
			acceptedIDs:  []string{"q-1"},
			answers:      map[string][]string{"q-1": {"UX Design"}},
			wantStatus:   conversation.StatusSuccess,
			wantResult:   `{"selected":["UX Design"]}`,
			wantShared:   true,
			wantFollowUp: true,
		},
		{
			name:         "answered in channel is terminal and streams follow-up immediately",
			channel:      openChannel,
			acceptedIDs:  []string{"q-1"},
			answers:      map[string][]string{"q-1": {"Design team"}},
			wantStatus:   conversation.StatusSuccess,
			wantResult:   `{"selected":["Design team"]}`,
			wantShared:   true,
			wantFollowUp: true,
		},
		{
			name:          "skipped question records decline without follow-up",
			channel:       openChannel,
			acceptedIDs:   []string{},
			wantStatus:    conversation.StatusRejected,
			wantResult:    "User skipped the question",
			wantShared:    false,
			wantFollowUp:  false,
			wantResultErr: true,
		},
		{
			name:        "invalid answer leaves the question pending",
			channel:     openChannel,
			acceptedIDs: []string{"q-1"},
			answers:     map[string][]string{"q-1": {"Engineering"}},
			wantErr:     ErrInvalidToolAnswer,
		},
		{
			name:        "missing answer leaves the question pending",
			channel:     openChannel,
			acceptedIDs: []string{"q-1"},
			answers:     nil,
			wantErr:     ErrInvalidToolAnswer,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()

			blocks := []conversation.ContentBlock{{
				Type:            conversation.BlockTypeToolUse,
				ID:              "q-1",
				Name:            "AskUserQuestion",
				Input:           questionInput,
				Status:          conversation.StatusPending,
				UserInteraction: llm.UserInteractionSelect,
				Shared:          conversation.BoolPtr(false),
			}}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			approvalPostID := "approval-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &approvalPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       1,
			}))

			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)
			botsService.SetBotsForTesting([]*bots.Bot{bot})

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:         mmClient,
				contextBuilder:   loadedStateBuilder(t),
				bots:             botsService,
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamingService,
			}

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)

			err = c.HandleToolCall(context.Background(), "user-id", approvalPost, tc.channel, tc.acceptedIDs, tc.answers)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				// The pending question must be untouched and answerable.
				turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
				require.NoError(t, turnsErr)
				require.Len(t, turns, 1)
				var unchanged []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[0].Content, &unchanged))
				assert.Equal(t, conversation.StatusPending, unchanged[0].Status)
				return
			}
			require.NoError(t, err)
			streamingService.waitForStreaming()

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, 2)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
			assert.Equal(t, tc.wantStatus, updatedBlocks[0].Status)
			if tc.wantShared {
				require.NotNil(t, updatedBlocks[0].Shared)
				assert.True(t, *updatedBlocks[0].Shared)
			}

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
			require.Len(t, resultBlocks, 1)
			assert.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			assert.Equal(t, "q-1", resultBlocks[0].ToolUseID)
			if tc.wantResultErr {
				assert.Equal(t, conversation.StatusError, resultBlocks[0].Status)
				assert.Contains(t, resultBlocks[0].Content, tc.wantResult)
			} else {
				assert.Equal(t, conversation.StatusSuccess, resultBlocks[0].Status)
				assert.JSONEq(t, tc.wantResult, resultBlocks[0].Content)
			}

			// Answer results are terminal: decided at write time, never
			// waiting on the share/keep-private stage.
			assert.NotNil(t, resultBlocks[0].DecidedAt)
			require.NotNil(t, resultBlocks[0].Shared)
			assert.Equal(t, tc.wantShared, *resultBlocks[0].Shared)

			if tc.wantFollowUp {
				assert.Len(t, lm.requests, 1, "expected a follow-up LLM request")
			} else {
				assert.Empty(t, lm.requests, "expected no follow-up LLM request")
			}
		})
	}
}
