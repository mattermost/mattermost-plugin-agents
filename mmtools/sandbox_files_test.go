// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeDownloader serves canned provider files, recording the ids requested in
// the order they were asked for.
type fakeDownloader struct {
	files     map[string]llm.ProviderFile
	err       error
	requested []string
}

func (d *fakeDownloader) DownloadProviderFile(_ context.Context, fileID string) (llm.ProviderFile, error) {
	d.requested = append(d.requested, fileID)
	if d.err != nil {
		return llm.ProviderFile{}, d.err
	}
	file, ok := d.files[fileID]
	if !ok {
		return llm.ProviderFile{}, errors.New("not found")
	}
	return file, nil
}

func sandboxCtx(channelID string, fileIDs ...string) *llm.Context {
	c := &llm.Context{
		Channel:        &model.Channel{Id: channelID},
		RequestingUser: &model.User{Id: "user-id"},
	}
	c.AddSandboxFileIDs(fileIDs...)
	return c
}

// TestAttachSandboxOutputFilesUploadsInObservationOrder covers the happy path:
// every file the sandbox captured is uploaded under the provider's own name, in
// the order the sandbox produced them, and recorded for response attachment.
func TestAttachSandboxOutputFilesUploadsInObservationOrder(t *testing.T) {
	const channelID = "channel-id"
	client := mocks.NewMockClient(t)
	client.On("GetConfig").Return(&model.Config{})
	client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)

	uploaded := map[string]string{}
	for _, name := range []string{"report.csv", "chart.png"} {
		fileName := name
		client.On("UploadFile", mock.Anything, fileName, channelID).Run(func(args mock.Arguments) {
			data, err := io.ReadAll(args.Get(0).(io.Reader))
			require.NoError(t, err)
			uploaded[fileName] = string(data)
		}).Return(&model.FileInfo{Id: "mm-" + fileName, Name: fileName}, nil).Once()
	}

	downloader := &fakeDownloader{files: map[string]llm.ProviderFile{
		"file_1": {Name: "report.csv", Content: []byte("a,b\n1,2\n")},
		"file_2": {Name: "chart.png", Content: []byte("PNG")},
	}}
	llmCtx := sandboxCtx(channelID, "file_1", "file_2")

	AttachSandboxOutputFiles(context.Background(), client, downloader, llmCtx)

	require.Equal(t, []string{"file_1", "file_2"}, downloader.requested)
	require.Equal(t, map[string]string{"report.csv": "a,b\n1,2\n", "chart.png": "PNG"}, uploaded)
	require.Equal(t, []llm.CreatedFile{
		{ID: "mm-report.csv", Name: "report.csv"},
		{ID: "mm-chart.png", Name: "chart.png"},
	}, llmCtx.CreatedFilesList())

	// Consumed: a second stream end must not attach the same files again.
	AttachSandboxOutputFiles(context.Background(), client, downloader, llmCtx)
	require.Equal(t, []string{"file_1", "file_2"}, downloader.requested)
}

// TestAttachSandboxOutputFilesRefusals covers every reason a turn's sandbox
// files must not reach the channel. Each case asserts no upload happens, which
// the mock enforces by having no UploadFile expectation.
func TestAttachSandboxOutputFilesRefusals(t *testing.T) {
	const channelID = "channel-id"

	tests := []struct {
		name       string
		llmCtx     func() *llm.Context
		config     *model.Config
		permission bool
		noConfig   bool
	}{
		{
			name:       "no sandbox files observed",
			llmCtx:     func() *llm.Context { return sandboxCtx(channelID) },
			permission: true,
			noConfig:   true,
		},
		{
			name: "no channel to hold the files",
			llmCtx: func() *llm.Context {
				c := &llm.Context{RequestingUser: &model.User{Id: "user-id"}}
				c.AddSandboxFileIDs("file_1")
				return c
			},
			permission: true,
		},
		{
			name: "no requesting user",
			llmCtx: func() *llm.Context {
				c := &llm.Context{Channel: &model.Channel{Id: channelID}}
				c.AddSandboxFileIDs("file_1")
				return c
			},
			permission: true,
		},
		{
			name:   "file attachments disabled on the server",
			llmCtx: func() *llm.Context { return sandboxCtx(channelID, "file_1") },
			config: &model.Config{FileSettings: model.FileSettings{
				EnableFileAttachments: model.NewPointer(false),
			}},
			permission: true,
		},
		{
			name:       "requesting user cannot upload to the channel",
			llmCtx:     func() *llm.Context { return sandboxCtx(channelID, "file_1") },
			permission: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mocks.NewMockClient(t)
			if !tt.noConfig {
				config := tt.config
				if config == nil {
					config = &model.Config{}
				}
				client.On("GetConfig").Return(config).Maybe()
				client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).
					Return(tt.permission).Maybe()
				client.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
			}

			downloader := &fakeDownloader{files: map[string]llm.ProviderFile{
				"file_1": {Name: "report.csv", Content: []byte("data")},
			}}
			llmCtx := tt.llmCtx()

			AttachSandboxOutputFiles(context.Background(), client, downloader, llmCtx)

			require.Empty(t, downloader.requested, "nothing may be downloaded when attachment is refused")
			require.Empty(t, llmCtx.CreatedFilesList())
		})
	}
}

// TestAttachSandboxOutputFilesSkipsBadFiles pins that one unusable file does not
// stop the rest: the reply still carries the files that were fine.
func TestAttachSandboxOutputFilesSkipsBadFiles(t *testing.T) {
	const channelID = "channel-id"

	tests := []struct {
		name string
		file llm.ProviderFile
	}{
		{name: "empty content", file: llm.ProviderFile{Name: "empty.txt", Content: nil}},
		{name: "over the size limit", file: llm.ProviderFile{Name: "big.bin", Content: []byte("12345678901")}},
		{name: "unusable name", file: llm.ProviderFile{Name: "..", Content: []byte("data")}},
		{name: "empty name", file: llm.ProviderFile{Name: "   ", Content: []byte("data")}},
		{
			name: "name over the length limit",
			file: llm.ProviderFile{Name: strings.Repeat("n", maxCreateFileNameLength+1), Content: []byte("data")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mocks.NewMockClient(t)
			client.On("GetConfig").Return(&model.Config{FileSettings: model.FileSettings{
				MaxFileSize: model.NewPointer(int64(10)),
			}})
			client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
			client.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Once()
			// Only the good file is uploaded.
			client.On("UploadFile", mock.Anything, "good.txt", channelID).
				Return(&model.FileInfo{Id: "mm-good", Name: "good.txt"}, nil).Once()

			downloader := &fakeDownloader{files: map[string]llm.ProviderFile{
				"file_bad":  tt.file,
				"file_good": {Name: "good.txt", Content: []byte("ok")},
			}}
			llmCtx := sandboxCtx(channelID, "file_bad", "file_good")

			AttachSandboxOutputFiles(context.Background(), client, downloader, llmCtx)

			require.Equal(t, []llm.CreatedFile{{ID: "mm-good", Name: "good.txt"}}, llmCtx.CreatedFilesList())
		})
	}
}

// TestAttachSandboxOutputFilesHonorsAttachmentCap pins that a sandbox run cannot
// push a post past the per-reply attachment cap, including slots already taken
// by CreateFile earlier in the same turn.
func TestAttachSandboxOutputFilesHonorsAttachmentCap(t *testing.T) {
	const channelID = "channel-id"
	client := mocks.NewMockClient(t)
	client.On("GetConfig").Return(&model.Config{})
	client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
	client.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Once()

	files := map[string]llm.ProviderFile{}
	var ids []string
	for i := range maxCreatedFilesPerTurn + 2 {
		id := "file_" + string(rune('a'+i))
		name := id + ".txt"
		ids = append(ids, id)
		files[id] = llm.ProviderFile{Name: name, Content: []byte("data")}
		client.On("UploadFile", mock.Anything, name, channelID).
			Return(&model.FileInfo{Id: "mm-" + id, Name: name}, nil).Maybe()
	}

	downloader := &fakeDownloader{files: files}
	llmCtx := sandboxCtx(channelID, ids...)

	AttachSandboxOutputFiles(context.Background(), client, downloader, llmCtx)

	require.Len(t, llmCtx.CreatedFilesList(), maxCreatedFilesPerTurn)
	require.Len(t, downloader.requested, maxCreatedFilesPerTurn,
		"files past the cap must not be downloaded at all")
}

// TestAttachSandboxOutputFilesSanitizesName pins that a provider-reported name
// is reduced to a safe base name before upload. The sandbox command that wrote
// the file is model-authored, so the name is model-influenced input — but a
// traversal-looking name still carries a usable base, so it uploads under that
// rather than being dropped (matching CreateFile).
func TestAttachSandboxOutputFilesSanitizesName(t *testing.T) {
	const channelID = "channel-id"

	tests := []struct {
		name         string
		providerName string
		uploadedName string
	}{
		{name: "traversal reduces to base name", providerName: "../../etc/passwd", uploadedName: "passwd"},
		{name: "absolute path reduces to base name", providerName: "/tmp/out/report.csv", uploadedName: "report.csv"},
		{name: "surrounding whitespace is trimmed", providerName: "  notes.txt  ", uploadedName: "notes.txt"},
		{
			name:         "multibyte name at the limit is kept because the limit counts characters",
			providerName: strings.Repeat("é", maxCreateFileNameLength),
			uploadedName: strings.Repeat("é", maxCreateFileNameLength),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mocks.NewMockClient(t)
			client.On("GetConfig").Return(&model.Config{})
			client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
			client.On("UploadFile", mock.Anything, tt.uploadedName, channelID).
				Return(&model.FileInfo{Id: "mm-1", Name: tt.uploadedName}, nil).Once()

			downloader := &fakeDownloader{files: map[string]llm.ProviderFile{
				"file_1": {Name: tt.providerName, Content: []byte("data")},
			}}
			llmCtx := sandboxCtx(channelID, "file_1")

			AttachSandboxOutputFiles(context.Background(), client, downloader, llmCtx)

			require.Equal(t, []llm.CreatedFile{{ID: "mm-1", Name: tt.uploadedName}}, llmCtx.CreatedFilesList())
		})
	}
}

// TestAttachSandboxOutputFilesDownloadFailure pins that a provider download
// failure is absorbed: the reply still goes out, just without the file.
func TestAttachSandboxOutputFilesDownloadFailure(t *testing.T) {
	const channelID = "channel-id"
	client := mocks.NewMockClient(t)
	client.On("GetConfig").Return(&model.Config{})
	client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true)
	client.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Once()

	downloader := &fakeDownloader{err: errors.New("provider exploded")}
	llmCtx := sandboxCtx(channelID, "file_1")

	AttachSandboxOutputFiles(context.Background(), client, downloader, llmCtx)

	require.Empty(t, llmCtx.CreatedFilesList())
}
