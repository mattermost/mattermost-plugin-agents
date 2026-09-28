// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDownloadFileAction = "download_file_attachment"

type fileActionPermissionCall struct {
	sessionID string
	fileID    string
	action    string
}

// fileActionPolicyClient describes the server API contract expected by the
// stop-gap. Keeping it test-local allows these tests to compile before the
// Mattermost dependency and mmapi.Client expose the new method.
type fileActionPolicyClient interface {
	mmapi.Client
	HasPermissionToFileAction(sessionID, fileID, action string) bool
}

type filePolicyProbeClient struct {
	mmapi.Client

	allowFileAction bool
	fileInfo        *model.FileInfo
	fileBody        string

	permissionCalls []fileActionPermissionCall
	fileInfoCalls   []string
	fileCalls       []string
	channelCalls    []string
}

var _ fileActionPolicyClient = (*filePolicyProbeClient)(nil)

func (c *filePolicyProbeClient) HasPermissionToFileAction(sessionID, fileID, action string) bool {
	c.permissionCalls = append(c.permissionCalls, fileActionPermissionCall{
		sessionID: sessionID,
		fileID:    fileID,
		action:    action,
	})
	return c.allowFileAction
}

func (c *filePolicyProbeClient) HasPermissionToChannel(userID, channelID string, _ *model.Permission) bool {
	c.channelCalls = append(c.channelCalls, userID+":"+channelID)
	return true
}

func (c *filePolicyProbeClient) GetFileInfo(fileID string) (*model.FileInfo, error) {
	c.fileInfoCalls = append(c.fileInfoCalls, fileID)
	return c.fileInfo, nil
}

func (c *filePolicyProbeClient) GetFile(fileID string) (io.ReadCloser, error) {
	c.fileCalls = append(c.fileCalls, fileID)
	return io.NopCloser(strings.NewReader(c.fileBody)), nil
}

func (c *filePolicyProbeClient) resetCalls() {
	c.permissionCalls = nil
	c.fileInfoCalls = nil
	c.fileCalls = nil
}

// userBlocksWithAttachmentsForSession supports the current legacy signature
// and the Phase-2 signature with sessionID appended. Reflection keeps Phase-1
// red tests buildable while the new server API is not in the Go dependency.
func userBlocksWithAttachmentsForSession(
	t *testing.T,
	message string,
	fileIDs []string,
	client fileActionPolicyClient,
	sessionID string,
) []ContentBlock {
	t.Helper()

	fn := reflect.ValueOf(userBlocksWithAttachments)
	args := []reflect.Value{
		reflect.ValueOf(message),
		reflect.ValueOf(fileIDs),
		reflect.ValueOf(client),
	}
	switch fn.Type().NumIn() {
	case 3:
	case 4:
		require.Equal(t, reflect.String, fn.Type().In(3).Kind(), "the appended attachment-policy argument must be sessionID")
		args = append(args, reflect.ValueOf(sessionID))
	default:
		t.Fatalf("unsupported userBlocksWithAttachments signature with %d arguments", fn.Type().NumIn())
	}

	return fn.Call(args)[0].Interface().([]ContentBlock)
}

// blocksToPostForSession supports the current legacy signature and the Phase-2
// signature with sessionID appended. The production implementation must pass
// that value to HasPermissionToFileAction before any admin-level file read.
func blocksToPostForSession(
	t *testing.T,
	blocks []ContentBlock,
	client fileActionPolicyClient,
	sessionID string,
) llm.Post {
	t.Helper()

	fn := reflect.ValueOf(BlocksToPost)
	args := []reflect.Value{
		reflect.ValueOf(blocks),
		reflect.ValueOf("user"),
		reflect.ValueOf(false),
		reflect.ValueOf(client),
		reflect.ValueOf(true),
		reflect.ValueOf(int64(0)),
	}
	switch fn.Type().NumIn() {
	case 6:
	case 7:
		require.Equal(t, reflect.String, fn.Type().In(6).Kind(), "the appended attachment-policy argument must be sessionID")
		args = append(args, reflect.ValueOf(sessionID))
	default:
		t.Fatalf("unsupported BlocksToPost signature with %d arguments", fn.Type().NumIn())
	}

	return fn.Call(args)[0].Interface().(llm.Post)
}

func TestDeniedAttachmentsDoNotReachAdminFileReadsOrLLM(t *testing.T) {
	const (
		sessionID = "session-for-request"
		userID    = "user-who-posted"
		channelID = "channel-readable-by-user"
		fileID    = "policy-denied-file"
		secret    = "policy-denied-file-content"
	)
	require.NotEqual(t, userID, sessionID, "the test must detect substituting user ID for session ID")

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

			require.True(t, client.HasPermissionToChannel(userID, channelID, model.PermissionReadChannel),
				"channel access is deliberately allowed; file policy must still be enforced independently")
			require.Equal(t, []string{userID + ":" + channelID}, client.channelCalls)

			blocks := userBlocksWithAttachmentsForSession(t, "inspect this", []string{fileID}, client, sessionID)

			assert.Equal(t, []fileActionPermissionCall{{
				sessionID: sessionID,
				fileID:    fileID,
				action:    testDownloadFileAction,
			}}, client.permissionCalls, "attachment classification must authorize with the request session")
			assert.Empty(t, client.fileInfoCalls, "denied attachments must not reach admin GetFileInfo during turn creation")
			assert.Empty(t, client.fileCalls, "denied attachments must not reach admin GetFile during turn creation")
			assert.Equal(t, []ContentBlock{{Type: BlockTypeText, Text: "inspect this"}}, blocks,
				"denied attachment references must not be persisted for later LLM conversion")

			client.resetCalls()
			post := blocksToPostForSession(t, []ContentBlock{
				{Type: BlockTypeText, Text: "inspect this"},
				tt.block,
			}, client, sessionID)

			assert.Equal(t, []fileActionPermissionCall{{
				sessionID: sessionID,
				fileID:    fileID,
				action:    testDownloadFileAction,
			}}, client.permissionCalls, "LLM conversion must independently authorize stored attachment references")
			assert.Empty(t, client.fileInfoCalls, "denied attachments must not reach admin GetFileInfo during LLM conversion")
			assert.Empty(t, client.fileCalls, "denied attachments must not reach admin GetFile during LLM conversion")
			assert.Empty(t, post.Files, "denied image readers must not be passed to the LLM")
			assert.NotContains(t, post.Message, secret, "denied file content must not be passed to the LLM")
			assert.Equal(t, "inspect this", post.Message, "authorized text must remain while the denied attachment is omitted")
		})
	}
}
