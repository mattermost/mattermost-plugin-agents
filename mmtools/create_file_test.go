// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func createFileArgsGetter(t *testing.T, args CreateFileArgs) llm.ToolArgumentGetter {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return func(out any) error {
		return json.Unmarshal(raw, out)
	}
}

func TestNewCreateFileTool(t *testing.T) {
	tool := NewCreateFileTool(mocks.NewMockClient(t))

	require.Equal(t, "CreateFile", tool.Name)
	require.True(t, tool.AutoExecute)
	require.Empty(t, tool.ServerOrigin)
	require.Empty(t, tool.UserInteraction)
	require.NotEmpty(t, tool.Description)
	require.NotNil(t, tool.Schema)
	require.NotNil(t, tool.Resolver)
}

// TestCreateFileResolverValidation covers every failure that must reject the
// call before any upload happens; the mock client asserts UploadFile is never
// called.
func TestCreateFileResolverValidation(t *testing.T) {
	validChannelCtx := func() *llm.Context {
		return &llm.Context{Channel: &model.Channel{Id: "channel-id"}}
	}
	validArgs := CreateFileArgs{FileName: "report.md", Content: "hello"}

	tests := []struct {
		name       string
		nilClient  bool
		llmCtx     func() *llm.Context
		args       CreateFileArgs
		badArgs    bool
		setup      func(m *mocks.MockClient)
		wantResult string
	}{
		{
			name:       "invalid arguments",
			llmCtx:     validChannelCtx,
			badArgs:    true,
			wantResult: "invalid parameters to function",
		},
		{
			name:       "nil client",
			nilClient:  true,
			llmCtx:     validChannelCtx,
			args:       validArgs,
			wantResult: "file creation is not available",
		},
		{
			name:       "nil context",
			llmCtx:     func() *llm.Context { return nil },
			args:       validArgs,
			wantResult: "file creation is not available in this context because there is no conversation channel to hold the file",
		},
		{
			name:       "nil channel",
			llmCtx:     func() *llm.Context { return &llm.Context{} },
			args:       validArgs,
			wantResult: "file creation is not available in this context because there is no conversation channel to hold the file",
		},
		{
			name:       "empty channel ID",
			llmCtx:     func() *llm.Context { return &llm.Context{Channel: &model.Channel{}} },
			args:       validArgs,
			wantResult: "file creation is not available in this context because there is no conversation channel to hold the file",
		},
		{
			name:       "empty file name",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: "", Content: "hello"},
			wantResult: "file_name must be a plain file name with an optional extension, e.g. report.md or data.csv",
		},
		{
			name:       "dot file name",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: ".", Content: "hello"},
			wantResult: "file_name must be a plain file name with an optional extension, e.g. report.md or data.csv",
		},
		{
			name:       "dot dot file name",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: "..", Content: "hello"},
			wantResult: "file_name must be a plain file name with an optional extension, e.g. report.md or data.csv",
		},
		{
			name:       "backslash separators survive Base and are rejected",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: `..\..\x`, Content: "hello"},
			wantResult: "file_name must be a plain file name with an optional extension, e.g. report.md or data.csv",
		},
		{
			name:       "file name too long",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: strings.Repeat("a", 256), Content: "hello"},
			wantResult: "file_name must be at most 255 characters",
		},
		{
			name:       "256-rune multibyte file name is rejected",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: strings.Repeat("é", 253) + ".md", Content: "hello"},
			wantResult: "file_name must be at most 255 characters",
		},
		{
			name:       "empty content",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: "report.md", Content: ""},
			wantResult: "content must not be empty",
		},
		{
			name:       "content over 1 MiB",
			llmCtx:     validChannelCtx,
			args:       CreateFileArgs{FileName: "report.md", Content: strings.Repeat("a", maxCreateFileContentBytes+1)},
			wantResult: "content exceeds the 1 MiB limit for a single file; split the content into multiple smaller files",
		},
		{
			name: "per-reply cap reached",
			llmCtx: func() *llm.Context {
				ctx := validChannelCtx()
				for i := 0; i < maxCreatedFilesPerTurn; i++ {
					ctx.AddCreatedFile(llm.CreatedFile{ID: model.NewId(), Name: fmt.Sprintf("f%d.txt", i)})
				}
				return ctx
			},
			args:       validArgs,
			wantResult: "the limit of 10 created files per reply has been reached; do not create more files in this reply",
		},
		{
			name:   "attachments disabled on the server",
			llmCtx: validChannelCtx,
			args:   validArgs,
			setup: func(m *mocks.MockClient) {
				m.On("GetConfig").Return(&model.Config{
					FileSettings: model.FileSettings{EnableFileAttachments: model.NewPointer(false)},
				})
			},
			wantResult: "file attachments are disabled on this server",
		},
		{
			name: "requesting user without upload permission in the channel",
			llmCtx: func() *llm.Context {
				ctx := validChannelCtx()
				ctx.RequestingUser = &model.User{Id: "user-id"}
				return ctx
			},
			args: validArgs,
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
			tool := NewCreateFileTool(nil)
			if !tt.nilClient {
				client := mocks.NewMockClient(t)
				if tt.setup != nil {
					tt.setup(client)
				}
				tool = NewCreateFileTool(client)
			}

			argsGetter := createFileArgsGetter(t, tt.args)
			if tt.badArgs {
				argsGetter = func(any) error { return errors.New("bad arguments") }
			}

			result, err := tool.Resolver(context.Background(), tt.llmCtx(), argsGetter)

			require.Error(t, err)
			require.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCreateFileResolverUpload(t *testing.T) {
	const channelID = "channel-id"

	tests := []struct {
		name string
		args CreateFileArgs
		// config is what GetConfig returns; nil means a config whose
		// EnableFileAttachments is unset, which must count as enabled.
		config         *model.Config
		requestingUser *model.User
		uploadedName   string
		uploadErr      error
	}{
		{
			name:         "happy path with nil requesting user (unattended flow)",
			args:         CreateFileArgs{FileName: "report.md", Content: "# Report\n\nBody."},
			config:       &model.Config{FileSettings: model.FileSettings{EnableFileAttachments: model.NewPointer(true)}},
			uploadedName: "report.md",
		},
		{
			name:           "requesting user with upload permission succeeds",
			args:           CreateFileArgs{FileName: "report.md", Content: "# Report"},
			config:         &model.Config{FileSettings: model.FileSettings{EnableFileAttachments: model.NewPointer(true)}},
			requestingUser: &model.User{Id: "user-id"},
			uploadedName:   "report.md",
		},
		{
			name:         "traversal name uploads the sanitized base name",
			args:         CreateFileArgs{FileName: "../../x", Content: "data"},
			uploadedName: "x",
		},
		{
			name:         "surrounding whitespace is trimmed",
			args:         CreateFileArgs{FileName: "  notes.txt  ", Content: "data"},
			uploadedName: "notes.txt",
		},
		{
			name:         "255-rune multibyte name is accepted because the limit counts characters, not bytes",
			args:         CreateFileArgs{FileName: strings.Repeat("é", 252) + ".md", Content: "data"},
			uploadedName: strings.Repeat("é", 252) + ".md",
		},
		{
			name:         "upload error",
			args:         CreateFileArgs{FileName: "report.md", Content: "# Report"},
			uploadedName: "report.md",
			uploadErr:    errors.New("s3 exploded"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileID := model.NewId()
			client := mocks.NewMockClient(t)
			config := tt.config
			if config == nil {
				// An unset EnableFileAttachments must be treated as enabled
				// (the Mattermost default).
				config = &model.Config{}
			}
			client.On("GetConfig").Return(config)
			if tt.requestingUser != nil {
				client.On("HasPermissionToChannel", tt.requestingUser.Id, channelID, model.PermissionUploadFile).Return(true)
			}
			var uploadedContent string
			call := client.On("UploadFile", mock.Anything, tt.uploadedName, channelID).Run(func(args mock.Arguments) {
				data, readErr := io.ReadAll(args.Get(0).(io.Reader))
				require.NoError(t, readErr)
				uploadedContent = string(data)
			}).Once()
			if tt.uploadErr != nil {
				call.Return(nil, tt.uploadErr)
			} else {
				call.Return(&model.FileInfo{Id: fileID, Name: tt.uploadedName}, nil)
			}

			llmCtx := &llm.Context{Channel: &model.Channel{Id: channelID}, RequestingUser: tt.requestingUser}
			tool := NewCreateFileTool(client)

			result, err := tool.Resolver(context.Background(), llmCtx, createFileArgsGetter(t, tt.args))

			if tt.uploadErr != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tt.uploadErr)
				require.Equal(t, "file upload failed", result)
				require.NotContains(t, result, tt.uploadErr.Error(), "model-facing message must not leak upload internals")
				require.Empty(t, llmCtx.CreatedFilesList())
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.args.Content, uploadedContent)

			parsed, ok := ParseCreateFileResult(result)
			require.True(t, ok)
			require.Equal(t, fileID, parsed.FileID)
			require.Equal(t, tt.uploadedName, parsed.FileName)
			require.NotEmpty(t, parsed.Note)

			require.Equal(t, []llm.CreatedFile{{ID: fileID, Name: tt.uploadedName}}, llmCtx.CreatedFilesList())
			require.Equal(t, llmCtx.CreatedFilesList(), ConsumeCreatedFiles(llmCtx))
		})
	}
}

func TestParseCreateFileResult(t *testing.T) {
	validID := model.NewId()
	valid, err := json.Marshal(CreateFileResult{FileID: validID, FileName: "report.md", Note: "note"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		content string
		want    CreateFileResult
		wantOK  bool
	}{
		{
			name:    "valid result",
			content: string(valid),
			want:    CreateFileResult{FileID: validID, FileName: "report.md", Note: "note"},
			wantOK:  true,
		},
		{
			name:    "invalid JSON",
			content: "file upload failed",
		},
		{
			name:    "missing file id",
			content: `{"file_name":"report.md"}`,
		},
		{
			name:    "invalid file id",
			content: `{"file_id":"not-a-valid-id","file_name":"report.md"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseCreateFileResult(tt.content)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestConsumeCreatedFiles(t *testing.T) {
	require.Nil(t, ConsumeCreatedFiles(nil))

	ctx := &llm.Context{}
	ctx.AddCreatedFile(llm.CreatedFile{ID: "file1", Name: "a.txt"})
	want := []llm.CreatedFile{{ID: "file1", Name: "a.txt"}}
	require.Equal(t, want, ConsumeCreatedFiles(ctx))
	require.Equal(t, want, ConsumeCreatedFiles(ctx), "reading must not remove the files")
}
