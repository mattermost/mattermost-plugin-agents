// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeDownloader implements llm.ProviderFileDownloader for resolver tests.
type fakeDownloader struct {
	content     []byte
	contentType string
	err         error
	calls       int
	gotFileID   string
}

func (f *fakeDownloader) DownloadProviderFile(_ context.Context, fileID string) ([]byte, string, error) {
	f.calls++
	f.gotFileID = fileID
	if f.err != nil {
		return nil, "", f.err
	}
	return f.content, f.contentType, nil
}

func attachArgsGetter(t *testing.T, args AttachSandboxFileArgs) llm.ToolArgumentGetter {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return func(out any) error {
		return json.Unmarshal(raw, out)
	}
}

func TestNewAttachSandboxFileTool(t *testing.T) {
	tool := NewAttachSandboxFileTool(mocks.NewMockClient(t), &fakeDownloader{})

	require.Equal(t, "AttachSandboxFile", tool.Name)
	require.True(t, tool.AutoExecute)
	require.Empty(t, tool.ServerOrigin)
	require.Empty(t, tool.UserInteraction)
	require.NotEmpty(t, tool.Description)
	require.NotNil(t, tool.Schema)
	require.NotNil(t, tool.Resolver)
}

// TestAttachSandboxFileResolverValidation covers every failure that must
// reject the call before any download or upload happens; the mock client and
// the fake downloader assert neither is reached.
func TestAttachSandboxFileResolverValidation(t *testing.T) {
	sandboxCtx := func() *llm.Context {
		c := &llm.Context{
			Channel:        &model.Channel{Id: "channel-id"},
			RequestingUser: &model.User{Id: "user-id"},
		}
		c.AddSandboxFileIDs("file_ok")
		return c
	}
	validArgs := AttachSandboxFileArgs{FileID: "file_ok", FileName: "report.csv"}

	tests := []struct {
		name          string
		nilClient     bool
		nilDownloader bool
		llmCtx        func() *llm.Context
		args          AttachSandboxFileArgs
		badArgs       bool
		setup         func(m *mocks.MockClient)
		wantResult    string
	}{
		{
			name:       "invalid arguments",
			llmCtx:     sandboxCtx,
			badArgs:    true,
			wantResult: "invalid parameters to function",
		},
		{
			name:       "nil client",
			nilClient:  true,
			llmCtx:     sandboxCtx,
			args:       validArgs,
			wantResult: "sandbox file attachment is not available",
		},
		{
			name:          "nil downloader",
			nilDownloader: true,
			llmCtx:        sandboxCtx,
			args:          validArgs,
			wantResult:    "sandbox file attachment is not available",
		},
		{
			name:       "nil context",
			llmCtx:     func() *llm.Context { return nil },
			args:       validArgs,
			wantResult: "file attachment is not available in this context because there is no conversation channel to hold the file",
		},
		{
			name:       "nil channel",
			llmCtx:     func() *llm.Context { return &llm.Context{} },
			args:       validArgs,
			wantResult: "file attachment is not available in this context because there is no conversation channel to hold the file",
		},
		{
			name: "nil requesting user fails closed",
			llmCtx: func() *llm.Context {
				return &llm.Context{Channel: &model.Channel{Id: "channel-id"}}
			},
			args:       validArgs,
			wantResult: "file attachment is not available in this context",
		},
		{
			name:       "unobserved file id rejected",
			llmCtx:     sandboxCtx,
			args:       AttachSandboxFileArgs{FileID: "file_never_seen", FileName: "report.csv"},
			wantResult: "file_id was not produced by the code execution sandbox in this conversation turn; pass a file_id exactly as it appears in a code execution result",
		},
		{
			name:       "empty file id rejected",
			llmCtx:     sandboxCtx,
			args:       AttachSandboxFileArgs{FileID: "", FileName: "report.csv"},
			wantResult: "file_id was not produced by the code execution sandbox in this conversation turn; pass a file_id exactly as it appears in a code execution result",
		},
		{
			name:       "path-traversal file name rejected",
			llmCtx:     sandboxCtx,
			args:       AttachSandboxFileArgs{FileID: "file_ok", FileName: ".."},
			wantResult: "file_name must be a plain file name with an optional extension, e.g. report.csv or chart.png",
		},
		{
			name:       "over-long file name rejected",
			llmCtx:     sandboxCtx,
			args:       AttachSandboxFileArgs{FileID: "file_ok", FileName: strings.Repeat("n", maxCreateFileNameLength+1) + ".csv"},
			wantResult: "file_name must be at most 255 characters",
		},
		{
			name:   "attachments disabled on server",
			llmCtx: sandboxCtx,
			args:   validArgs,
			setup: func(m *mocks.MockClient) {
				m.On("GetConfig").Return(&model.Config{
					FileSettings: model.FileSettings{EnableFileAttachments: model.NewPointer(false)},
				})
			},
			wantResult: "file attachments are disabled on this server",
		},
		{
			name: "per-reply attachment cap reached",
			llmCtx: func() *llm.Context {
				c := sandboxCtx()
				c.SetResponseAttachmentBudget(-1)
				return c
			},
			args: validArgs,
			setup: func(m *mocks.MockClient) {
				m.On("GetConfig").Return(&model.Config{
					FileSettings: model.FileSettings{EnableFileAttachments: model.NewPointer(true)},
				})
			},
			wantResult: "no more files can be attached to this reply (limit 10 per post); do not attach more files in this reply",
		},
		{
			name:   "missing upload permission",
			llmCtx: sandboxCtx,
			args:   validArgs,
			setup: func(m *mocks.MockClient) {
				m.On("GetConfig").Return(&model.Config{
					FileSettings: model.FileSettings{EnableFileAttachments: model.NewPointer(true)},
				})
				m.On("HasPermissionToChannel", "user-id", "channel-id", model.PermissionUploadFile).Return(false)
			},
			wantResult: "you do not have permission to attach files in this channel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var client *mocks.MockClient
			if !tt.nilClient {
				client = mocks.NewMockClient(t)
				if tt.setup != nil {
					tt.setup(client)
				}
			}
			downloader := &fakeDownloader{content: []byte("data")}

			var argsGetter llm.ToolArgumentGetter
			if tt.badArgs {
				argsGetter = func(any) error { return errors.New("bad args") }
			} else {
				argsGetter = attachArgsGetter(t, tt.args)
			}

			var mmClient *mocks.MockClient
			if client != nil {
				mmClient = client
			}
			var dl llm.ProviderFileDownloader = downloader
			if tt.nilDownloader {
				dl = nil
			}

			var result string
			var err error
			if mmClient == nil {
				result, err = resolveAttachSandboxFile(context.Background(), nil, dl, tt.llmCtx(), argsGetter)
			} else {
				result, err = resolveAttachSandboxFile(context.Background(), mmClient, dl, tt.llmCtx(), argsGetter)
			}

			require.Error(t, err)
			require.Equal(t, tt.wantResult, result)
			require.Zero(t, downloader.calls, "the provider download must not run for rejected calls")
		})
	}
}

func TestAttachSandboxFileDownloadAndUpload(t *testing.T) {
	channelID := "channel-id"
	newCtx := func() *llm.Context {
		c := &llm.Context{
			Channel:        &model.Channel{Id: channelID},
			RequestingUser: &model.User{Id: "user-id"},
		}
		c.AddSandboxFileIDs("file_ok")
		return c
	}
	enabledConfig := &model.Config{
		FileSettings: model.FileSettings{
			EnableFileAttachments: model.NewPointer(true),
			MaxFileSize:           model.NewPointer(int64(16)),
		},
	}

	t.Run("download failure surfaces without uploading", func(t *testing.T) {
		client := mocks.NewMockClient(t)
		client.On("GetConfig").Return(enabledConfig)
		client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
		downloader := &fakeDownloader{err: errors.New("provider unavailable")}

		result, err := resolveAttachSandboxFile(context.Background(), client, downloader,
			newCtx(), attachArgsGetter(t, AttachSandboxFileArgs{FileID: "file_ok", FileName: "report.csv"}))

		require.Error(t, err)
		require.Equal(t, "downloading the file from the sandbox failed", result)
		require.Equal(t, "file_ok", downloader.gotFileID)
	})

	t.Run("oversized download rejected without uploading", func(t *testing.T) {
		client := mocks.NewMockClient(t)
		client.On("GetConfig").Return(enabledConfig)
		client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
		downloader := &fakeDownloader{content: []byte(strings.Repeat("x", 17))}

		result, err := resolveAttachSandboxFile(context.Background(), client, downloader,
			newCtx(), attachArgsGetter(t, AttachSandboxFileArgs{FileID: "file_ok", FileName: "report.csv"}))

		require.Error(t, err)
		require.Equal(t, "the file exceeds the 16-byte file size limit and cannot be attached", result)
	})

	t.Run("empty download rejected without uploading", func(t *testing.T) {
		client := mocks.NewMockClient(t)
		client.On("GetConfig").Return(enabledConfig)
		client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
		downloader := &fakeDownloader{content: nil}

		result, err := resolveAttachSandboxFile(context.Background(), client, downloader,
			newCtx(), attachArgsGetter(t, AttachSandboxFileArgs{FileID: "file_ok", FileName: "report.csv"}))

		require.Error(t, err)
		require.Equal(t, "the sandbox file is empty", result)
	})

	t.Run("success uploads bytes and records the created file", func(t *testing.T) {
		// Must be a well-formed Mattermost id so ParseCreateFileResult accepts it.
		uploadedFileID := model.NewId()
		client := mocks.NewMockClient(t)
		client.On("GetConfig").Return(enabledConfig)
		client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
		var uploaded []byte
		client.On("UploadFile", mock.MatchedBy(func(r any) bool {
			reader, ok := r.(interface{ Read([]byte) (int, error) })
			if !ok {
				return false
			}
			buf := make([]byte, 64)
			n, _ := reader.Read(buf)
			uploaded = append([]byte(nil), buf[:n]...)
			return true
		}), "report.csv", channelID).Return(&model.FileInfo{Id: uploadedFileID, Name: "report.csv"}, nil)

		llmCtx := newCtx()
		downloader := &fakeDownloader{content: []byte("col1,col2"), contentType: "text/csv"}

		result, err := resolveAttachSandboxFile(context.Background(), client, downloader,
			llmCtx, attachArgsGetter(t, AttachSandboxFileArgs{FileID: "file_ok", FileName: "report.csv"}))

		require.NoError(t, err)
		require.Equal(t, []byte("col1,col2"), uploaded)

		parsed, ok := ParseCreateFileResult(result)
		require.True(t, ok, "result must parse as a CreateFileResult so the attachment fallback works")
		require.Equal(t, uploadedFileID, parsed.FileID)
		require.Equal(t, "report.csv", parsed.FileName)

		created := llmCtx.CreatedFilesList()
		require.Len(t, created, 1)
		require.Equal(t, uploadedFileID, created[0].ID)
	})
}
