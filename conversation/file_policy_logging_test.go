// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type completionRequestCaptureModel struct {
	requests []llm.CompletionRequest
}

func (m *completionRequestCaptureModel) ChatCompletion(request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	m.requests = append(m.requests, request)
	return &llm.TextStreamResult{}, nil
}

func (m *completionRequestCaptureModel) ChatCompletionNoStream(request llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	m.requests = append(m.requests, request)
	return "", nil
}

func (m *completionRequestCaptureModel) CountTokens(string) int {
	return 0
}

func (m *completionRequestCaptureModel) InputTokenLimit() int {
	return 0
}

func TestDeniedAttachmentsStayOutOfLLMLoggingPipeline(t *testing.T) {
	const (
		sessionID = "session-for-request"
		fileID    = "policy-denied-file"
		secret    = "policy-denied-file-content"
	)

	tests := []struct {
		name     string
		block    ContentBlock
		fileInfo *model.FileInfo
	}{
		{
			name:  "text attachment",
			block: ContentBlock{Type: BlockTypeFile, FileID: fileID, Filename: "secret.txt", MimeType: "text/plain"},
			fileInfo: &model.FileInfo{
				Id: fileID, Name: "secret.txt", MimeType: "text/plain", Size: int64(len(secret)),
			},
		},
		{
			name:  "vision attachment",
			block: ContentBlock{Type: BlockTypeImage, FileID: fileID, Filename: "secret.png", MimeType: "image/png"},
			fileInfo: &model.FileInfo{
				Id: fileID, Name: "secret.png", MimeType: "image/png", Size: int64(len(secret)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &filePolicyProbeClient{
				allowFileAction: false,
				fileInfo:        tt.fileInfo,
				fileBody:        secret,
			}

			newTurnBlocks := userBlocksWithAttachmentsForSession(t, "inspect this", []string{fileID}, client, sessionID)
			require.Equal(t, []ContentBlock{{Type: BlockTypeText, Text: "inspect this"}}, newTurnBlocks)
			assert.Empty(t, client.fileInfoCalls)
			assert.Empty(t, client.fileCalls)

			newTurnPost := blocksToPostForSession(t, newTurnBlocks, client, sessionID)

			client.resetCalls()
			storedTurnPost := blocksToPostForSession(t, []ContentBlock{
				{Type: BlockTypeText, Text: "inspect this"},
				tt.block,
			}, client, sessionID)
			require.Equal(t, []fileActionPermissionCall{{
				sessionID: sessionID,
				fileID:    fileID,
				action:    testDownloadFileAction,
			}}, client.permissionCalls)
			assert.Empty(t, client.fileInfoCalls)
			assert.Empty(t, client.fileCalls)

			var logs []string
			mockAPI := &plugintest.API{}
			mockAPI.On(
				"LogInfo",
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
			).Run(func(args mock.Arguments) {
				logs = append(logs, fmt.Sprint(args...))
			}).Return()

			captureModel := &completionRequestCaptureModel{}
			logger := pluginapi.NewClient(mockAPI, nil).Log
			loggingModel := llm.NewLanguageModelLogWrapper(logger, captureModel)
			for _, post := range []llm.Post{newTurnPost, storedTurnPost} {
				_, err := loggingModel.ChatCompletion(llm.CompletionRequest{
					Posts:   []llm.Post{post},
					Context: &llm.Context{},
				})
				require.NoError(t, err)
			}

			require.Len(t, captureModel.requests, 2)
			require.Len(t, logs, 2)
			for _, request := range captureModel.requests {
				assert.Empty(t, request.Posts[0].Files)
				assert.NotContains(t, request.Posts[0].Message, tt.fileInfo.Name)
				assert.NotContains(t, request.Posts[0].Message, secret)
			}
			for _, entry := range logs {
				assert.NotContains(t, entry, tt.fileInfo.Name)
				assert.NotContains(t, entry, secret)
			}
		})
	}
}
