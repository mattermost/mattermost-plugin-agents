// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/require"
)

// TestMeetingSummaryFollowUpRechecksCaptionsChannelAccess covers a DM
// follow-up in a meeting summary thread whose stored first turn references a
// captions file posted in the meeting channel, not the DM.
func TestMeetingSummaryFollowUpRechecksCaptionsChannelAccess(t *testing.T) {
	const (
		meetingChannelID = "meeting-channel"
		captionsFileID   = "captions-file"
		summaryRootID    = "summary-root-post"
		transcriptPhrase = "TRANSCRIPT_ONLY_PHRASE we ship on Friday"
		storedSummary    = "Stored summary: the team ships at the end of the week."
		followUpQuestion = "Who writes the release notes?"
	)
	captions := "WEBVTT\n\n1\n00:00:00.000 --> 00:00:02.000\n<v Alice>" + transcriptPhrase + ".\n"

	tests := []struct {
		name               string
		meetingUnreadable  bool
		wantTranscriptSent bool
	}{
		{name: "owner can still read the meeting channel", wantTranscriptSent: true},
		{name: "owner lost read access to the meeting channel", meetingUnreadable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupDMTestEnv(t, dmMakeTextStream("follow-up answer"))
			env.mmClient.fileInfos = map[string]*model.FileInfo{
				captionsFileID: {Id: captionsFileID, ChannelId: meetingChannelID, Name: "captions.vtt", MimeType: "text/vtt", Size: int64(len(captions))},
			}
			env.mmClient.files = map[string][]byte{captionsFileID: []byte(captions)}
			env.mmClient.unreadableChannels = map[string]bool{meetingChannelID: tt.meetingUnreadable}

			promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
			require.NoError(t, err)
			env.conversations.SetConversationService(conversation.NewService(env.convStore, promptsManager, env.mmClient, env.botService))

			seedMeetingSummaryConversation(t, env, summaryRootID, captionsFileID, storedSummary)

			env.conversations.MessageHasBeenPosted(&plugin.Context{SessionId: model.NewId()}, &model.Post{
				Id:        model.NewId(),
				UserId:    env.userID,
				ChannelId: env.channelID,
				RootId:    summaryRootID,
				Message:   followUpQuestion,
			})

			env.fakeLLM.mu.Lock()
			defer env.fakeLLM.mu.Unlock()
			require.Len(t, env.fakeLLM.requests, 1, "logged errors: %v", env.mmClient.loggedErrors())
			var sent strings.Builder
			for _, post := range env.fakeLLM.requests[0].Posts {
				sent.WriteString(post.Message)
				sent.WriteString("\n")
			}
			require.Contains(t, sent.String(), storedSummary)
			require.Contains(t, sent.String(), followUpQuestion)
			if tt.wantTranscriptSent {
				require.Contains(t, sent.String(), transcriptPhrase)
				return
			}
			require.NotContains(t, sent.String(), transcriptPhrase)
			require.NotContains(t, sent.String(), captionsFileID, "an unreadable file must not even be described")
		})
	}
}

// seedMeetingSummaryConversation stores the turns a captions meeting summary
// leaves behind: a user turn referencing the captions file and the summary.
func seedMeetingSummaryConversation(t *testing.T, env *dmTestEnv, rootID, fileID, summary string) {
	t.Helper()
	channelID := env.channelID
	convID := model.NewId()
	if env.mmClient.posts == nil {
		env.mmClient.posts = map[string]*model.Post{}
	}
	env.mmClient.posts[rootID] = &model.Post{Id: rootID, UserId: env.botID, ChannelId: channelID, Message: summary}
	require.NoError(t, env.convStore.CreateConversation(&store.Conversation{
		ID:           convID,
		UserID:       env.userID,
		BotID:        env.botID,
		ChannelID:    &channelID,
		RootPostID:   &rootID,
		Operation:    llm.OperationMeetingSummary,
		SystemPrompt: "Summarize the meeting.",
	}))

	userBlocks, err := json.Marshal([]conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: "Summarize the meeting transcript."},
		{Type: conversation.BlockTypeFile, FileID: fileID, Filename: "captions.vtt", MimeType: "text/vtt"},
	})
	require.NoError(t, err)
	require.NoError(t, env.convStore.CreateTurnAutoSequence(&store.Turn{
		ID: model.NewId(), ConversationID: convID, Role: "user", Content: userBlocks,
	}))

	summaryBlocks, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: summary}})
	require.NoError(t, err)
	require.NoError(t, env.convStore.CreateTurnAutoSequence(&store.Turn{
		ID: model.NewId(), ConversationID: convID, PostID: &rootID, Role: "assistant", Content: summaryBlocks,
	}))
}
