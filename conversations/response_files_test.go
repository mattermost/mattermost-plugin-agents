// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type responseFilesDownloader struct {
	requested []llm.ProviderFileReference
}

func (d *responseFilesDownloader) DownloadProviderFile(_ context.Context, ref llm.ProviderFileReference, _ int64) (llm.ProviderFile, error) {
	d.requested = append(d.requested, ref)
	return llm.ProviderFile{Name: ref.ID + ".txt", Content: []byte("content")}, nil
}

func makeEventStream(events ...llm.TextStreamEvent) *llm.TextStreamResult {
	ch := make(chan llm.TextStreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &llm.TextStreamResult{Stream: ch}
}

func ctxWithCreatedFiles(ids ...string) *llm.Context {
	c := &llm.Context{}
	for _, id := range ids {
		c.AddCreatedFile(llm.CreatedFile{ID: id, Name: id + ".md"})
	}
	return c
}

func eventTypes(events []llm.TextStreamEvent) []llm.EventType {
	types := make([]llm.EventType, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}
	return types
}

func TestDecorateStreamWithCreatedFiles(t *testing.T) {
	textEvent := llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Here is your file."}
	endEvent := llm.TextStreamEvent{Type: llm.EventTypeEnd}

	manyExisting := make([]string, 8)
	for i := range manyExisting {
		manyExisting[i] = fmt.Sprintf("existing-%d", i)
	}

	tests := []struct {
		name           string
		events         []llm.TextStreamEvent
		contexts       []*llm.Context
		extraFileIDs   []string
		postFileIDs    []string
		wantFilesEvent []string
		wantEventTypes []llm.EventType
	}{
		{
			name:           "context files announced immediately before end",
			events:         []llm.TextStreamEvent{textEvent, endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1", "f2")},
			wantFilesEvent: []string{"f1", "f2"},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "no created files: no files event",
			events:         []llm.TextStreamEvent{textEvent, endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles()},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeEnd},
		},
		{
			name:           "nil contexts tolerated",
			events:         []llm.TextStreamEvent{textEvent, endEvent},
			contexts:       []*llm.Context{nil, ctxWithCreatedFiles("f1"), nil},
			wantFilesEvent: []string{"f1"},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "extra file IDs merged and deduped against contexts and existing attachments",
			events:         []llm.TextStreamEvent{endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1", "f2")},
			extraFileIDs:   []string{"f2", "f3", "attached", "f1"},
			postFileIDs:    []string{"attached"},
			wantFilesEvent: []string{"f1", "f2", "f3"},
			wantEventTypes: []llm.EventType{llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "candidates truncated to the attachment cap minus existing",
			events:         []llm.TextStreamEvent{endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1", "f2", "f3", "f4", "f5")},
			postFileIDs:    manyExisting,
			wantFilesEvent: []string{"f1", "f2"},
			wantEventTypes: []llm.EventType{llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "error stream passes through without a files event",
			events:         []llm.TextStreamEvent{textEvent, {Type: llm.EventTypeError, Value: errors.New("boom")}},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1")},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeError},
		},
		{
			name:           "channel close without end passes through without a files event",
			events:         []llm.TextStreamEvent{textEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1")},
			wantEventTypes: []llm.EventType{llm.EventTypeText},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Conversations{}
			post := &model.Post{Id: "post-id", ChannelId: "channel-id", FileIds: tt.postFileIDs}

			decorated := c.decorateStreamWithCreatedFiles(context.Background(), nil, makeEventStream(tt.events...), post, tt.extraFileIDs, nil, tt.contexts...)
			events := drainTextStreamEvents(t, decorated)

			assert.Equal(t, tt.wantEventTypes, eventTypes(events))

			var filesEvent *llm.TextStreamEvent
			for i := range events {
				if events[i].Type == llm.EventTypeFiles {
					filesEvent = &events[i]
				}
			}
			if tt.wantFilesEvent == nil {
				assert.Nil(t, filesEvent, "no files event expected")
			} else {
				require.NotNil(t, filesEvent, "expected a files event")
				assert.Equal(t, tt.wantFilesEvent, filesEvent.Value)
			}
		})
	}

	t.Run("nil stream returns nil", func(t *testing.T) {
		c := &Conversations{}
		assert.Nil(t, c.decorateStreamWithCreatedFiles(context.Background(), nil, nil, &model.Post{}, nil, nil))
	})
}

func TestDecorateStreamAttachesSandboxFilesOnlyFromActiveContext(t *testing.T) {
	const channelID = "channel-id"

	client := mocks.NewMockClient(t)
	client.On("GetConfig").Return(&model.Config{}).Twice()
	client.On("HasPermissionToChannel", "user-id", channelID, model.PermissionUploadFile).Return(true).Once()
	client.On("UploadFile", mock.Anything, "active.txt", channelID).
		Return(&model.FileInfo{Id: "mm-active", Name: "active.txt"}, nil).Once()

	downloader := &responseFilesDownloader{}
	bot := bots.NewBot(
		llm.BotConfig{EnabledNativeTools: []string{llm.NativeToolCodeInterpreter}},
		llm.ServiceConfig{Type: llm.ServiceTypeAnthropic},
		&model.Bot{},
		nil,
	)
	bot.SetProviderServicesForTest(&llm.ProviderServices{FileDownloader: downloader})

	activeContext := &llm.Context{
		Channel:        &model.Channel{Id: channelID},
		RequestingUser: &model.User{Id: "user-id"},
	}
	activeContext.AddSandboxFiles(llm.ProviderFileReference{ID: "active", ProviderRoute: "anthropic"})

	historicalContext := ctxWithCreatedFiles("historical-created")
	historicalContext.AddSandboxFiles(llm.ProviderFileReference{ID: "historical", ProviderRoute: "anthropic::old"})

	c := &Conversations{mmClient: client}
	decorated := c.decorateStreamWithCreatedFiles(
		context.Background(),
		bot,
		makeEventStream(llm.TextStreamEvent{Type: llm.EventTypeEnd}),
		&model.Post{Id: "post-id", ChannelId: channelID},
		nil,
		activeContext,
		activeContext,
		historicalContext,
	)
	events := drainTextStreamEvents(t, decorated)

	require.Equal(t, []llm.ProviderFileReference{{ID: "active", ProviderRoute: "anthropic"}}, downloader.requested)
	require.Empty(t, activeContext.ConsumeSandboxFiles())
	require.Equal(t, []llm.ProviderFileReference{{ID: "historical", ProviderRoute: "anthropic::old"}}, historicalContext.ConsumeSandboxFiles(),
		"historical provider files must remain untouched")
	require.Equal(t, []llm.EventType{llm.EventTypeFiles, llm.EventTypeEnd}, eventTypes(events))
	require.Equal(t, []string{"mm-active", "historical-created"}, events[0].Value,
		"historical contexts remain eligible only for previously created Mattermost files")
}

func createFileResultJSON(t *testing.T, fileID string) string {
	t.Helper()
	content, err := json.Marshal(mmtools.CreateFileResult{FileID: fileID, FileName: "report.md"})
	require.NoError(t, err)
	return string(content)
}

func turnWithBlocks(t *testing.T, role string, postID *string, seq int, blocks []conversation.ContentBlock) store.Turn {
	t.Helper()
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	return store.Turn{
		ID:       model.NewId(),
		PostID:   postID,
		Role:     role,
		Content:  content,
		Sequence: seq,
	}
}

func createFileUseBlock(id, serverOrigin string) conversation.ContentBlock {
	return conversation.ContentBlock{
		Type:         conversation.BlockTypeToolUse,
		ID:           id,
		Name:         mmtools.CreateFileToolName,
		ServerOrigin: serverOrigin,
		Status:       conversation.StatusAutoApproved,
	}
}

func createFileResultBlock(toolUseID, status, content string) conversation.ContentBlock {
	return conversation.ContentBlock{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: toolUseID,
		Status:    status,
		Content:   content,
	}
}

func TestCreatedFileIDsFromTurnWindow(t *testing.T) {
	currentPostID := "current-post"
	oldPostID := "old-post"
	fileID := model.NewId()
	secondFileID := model.NewId()
	oldFileID := model.NewId()

	// A full previous run whose created file must never leak into the
	// current run's scan.
	previousRun := func(t *testing.T) []store.Turn {
		return []store.Turn{
			turnWithBlocks(t, "user", nil, 1, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "make me a file"}}),
			turnWithBlocks(t, "assistant", nil, 2, []conversation.ContentBlock{createFileUseBlock("old-use", "")}),
			turnWithBlocks(t, "tool_result", nil, 3, []conversation.ContentBlock{
				createFileResultBlock("old-use", conversation.StatusSuccess, createFileResultJSON(t, oldFileID)),
			}),
			turnWithBlocks(t, "assistant", &oldPostID, 4, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "done"}}),
		}
	}

	tests := []struct {
		name   string
		turns  func(t *testing.T) []store.Turn
		postID string
		want   []string
	}{
		{
			name: "successful CreateFile results in the current window are returned in order",
			turns: func(t *testing.T) []store.Turn {
				return append(previousRun(t),
					turnWithBlocks(t, "user", nil, 5, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "two more"}}),
					turnWithBlocks(t, "assistant", nil, 6, []conversation.ContentBlock{
						createFileUseBlock("use-1", ""),
						createFileUseBlock("use-2", ""),
					}),
					turnWithBlocks(t, "tool_result", nil, 7, []conversation.ContentBlock{
						createFileResultBlock("use-1", conversation.StatusSuccess, createFileResultJSON(t, fileID)),
						createFileResultBlock("use-2", conversation.StatusAutoApproved, createFileResultJSON(t, secondFileID)),
					}),
					turnWithBlocks(t, "assistant", &currentPostID, 8, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "done"}}),
				)
			},
			postID: currentPostID,
			want:   []string{fileID, secondFileID},
		},
		{
			name: "errored result is skipped",
			turns: func(t *testing.T) []store.Turn {
				return append(previousRun(t),
					turnWithBlocks(t, "user", nil, 5, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "again"}}),
					turnWithBlocks(t, "assistant", nil, 6, []conversation.ContentBlock{createFileUseBlock("use-1", "")}),
					turnWithBlocks(t, "tool_result", nil, 7, []conversation.ContentBlock{
						createFileResultBlock("use-1", conversation.StatusError, "file upload failed"),
					}),
					turnWithBlocks(t, "assistant", &currentPostID, 8, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "sorry"}}),
				)
			},
			postID: currentPostID,
			want:   nil,
		},
		{
			name: "MCP tool sharing the CreateFile name is skipped",
			turns: func(t *testing.T) []store.Turn {
				return append(previousRun(t),
					turnWithBlocks(t, "user", nil, 5, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "again"}}),
					turnWithBlocks(t, "assistant", nil, 6, []conversation.ContentBlock{createFileUseBlock("use-1", "https://mcp.example.com")}),
					turnWithBlocks(t, "tool_result", nil, 7, []conversation.ContentBlock{
						createFileResultBlock("use-1", conversation.StatusSuccess, createFileResultJSON(t, fileID)),
					}),
					turnWithBlocks(t, "assistant", &currentPostID, 8, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "done"}}),
				)
			},
			postID: currentPostID,
			want:   nil,
		},
		{
			name: "unparseable result content is skipped",
			turns: func(t *testing.T) []store.Turn {
				return append(previousRun(t),
					turnWithBlocks(t, "user", nil, 5, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "again"}}),
					turnWithBlocks(t, "assistant", nil, 6, []conversation.ContentBlock{createFileUseBlock("use-1", "")}),
					turnWithBlocks(t, "tool_result", nil, 7, []conversation.ContentBlock{
						createFileResultBlock("use-1", conversation.StatusSuccess, "not json at all"),
					}),
					turnWithBlocks(t, "assistant", &currentPostID, 8, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "done"}}),
				)
			},
			postID: currentPostID,
			want:   nil,
		},
		{
			name: "previous run's CreateFile is outside the window",
			turns: func(t *testing.T) []store.Turn {
				return append(previousRun(t),
					turnWithBlocks(t, "user", nil, 5, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "no files this time"}}),
					turnWithBlocks(t, "assistant", &currentPostID, 6, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "ok"}}),
				)
			},
			postID: currentPostID,
			want:   nil,
		},
		{
			name: "no anchor for post falls back to the last user turn's window",
			turns: func(t *testing.T) []store.Turn {
				return append(previousRun(t),
					turnWithBlocks(t, "user", nil, 5, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "make a file"}}),
					turnWithBlocks(t, "assistant", nil, 6, []conversation.ContentBlock{createFileUseBlock("use-1", "")}),
					turnWithBlocks(t, "tool_result", nil, 7, []conversation.ContentBlock{
						createFileResultBlock("use-1", conversation.StatusSuccess, createFileResultJSON(t, fileID)),
					}),
				)
			},
			postID: "post-with-no-turn",
			want:   []string{fileID},
		},
		{
			name: "unmarshalable turn content is tolerated",
			turns: func(t *testing.T) []store.Turn {
				turns := append(previousRun(t),
					turnWithBlocks(t, "user", nil, 5, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "again"}}),
					store.Turn{ID: model.NewId(), Role: "assistant", Content: json.RawMessage(`{invalid`), Sequence: 6},
					turnWithBlocks(t, "assistant", &currentPostID, 7, []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "done"}}),
				)
				return turns
			},
			postID: currentPostID,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, createdFileIDsFromTurnWindow(tt.turns(t), tt.postID))
		})
	}
}

func TestCollectCreatedFileIDsFromTurnsWithoutService(t *testing.T) {
	c := &Conversations{}
	assert.Nil(t, c.collectCreatedFileIDsFromTurns("conv-id", "post-id"))
}
