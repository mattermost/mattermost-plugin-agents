// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingLinker is a hand-rolled fileLinker stub that records every call.
type recordingLinker struct {
	calls      [][]string
	postIDs    []string
	channelIDs []string
	// respond overrides the default echo behavior (link everything).
	respond func(fileIDs []string) ([]string, error)
}

func (l *recordingLinker) link(fileIDs []string, postID, channelID string) ([]string, error) {
	l.calls = append(l.calls, append([]string(nil), fileIDs...))
	l.postIDs = append(l.postIDs, postID)
	l.channelIDs = append(l.channelIDs, channelID)
	if l.respond != nil {
		return l.respond(fileIDs)
	}
	return fileIDs, nil
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
		respond        func(fileIDs []string) ([]string, error)
		wantLinkCalls  [][]string
		wantFilesEvent []string
		wantEventTypes []llm.EventType
	}{
		{
			name:           "context files linked and announced immediately before end",
			events:         []llm.TextStreamEvent{textEvent, endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1", "f2")},
			wantLinkCalls:  [][]string{{"f1", "f2"}},
			wantFilesEvent: []string{"f1", "f2"},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "no created files: linker not called and no files event",
			events:         []llm.TextStreamEvent{textEvent, endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles()},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeEnd},
		},
		{
			name:           "nil contexts tolerated",
			events:         []llm.TextStreamEvent{textEvent, endEvent},
			contexts:       []*llm.Context{nil, ctxWithCreatedFiles("f1"), nil},
			wantLinkCalls:  [][]string{{"f1"}},
			wantFilesEvent: []string{"f1"},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "extra file IDs merged and deduped against contexts and existing attachments",
			events:         []llm.TextStreamEvent{endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1", "f2")},
			extraFileIDs:   []string{"f2", "f3", "attached", "f1"},
			postFileIDs:    []string{"attached"},
			wantLinkCalls:  [][]string{{"f1", "f2", "f3"}},
			wantFilesEvent: []string{"f1", "f2", "f3"},
			wantEventTypes: []llm.EventType{llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "candidates truncated to the attachment cap minus existing",
			events:         []llm.TextStreamEvent{endEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1", "f2", "f3", "f4", "f5")},
			postFileIDs:    manyExisting,
			wantLinkCalls:  [][]string{{"f1", "f2"}},
			wantFilesEvent: []string{"f1", "f2"},
			wantEventTypes: []llm.EventType{llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:     "linker returning a subset announces only the subset",
			events:   []llm.TextStreamEvent{endEvent},
			contexts: []*llm.Context{ctxWithCreatedFiles("f1", "f2")},
			respond: func([]string) ([]string, error) {
				return []string{"f2"}, nil
			},
			wantLinkCalls:  [][]string{{"f1", "f2"}},
			wantFilesEvent: []string{"f2"},
			wantEventTypes: []llm.EventType{llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:     "linker error with nothing linked: no files event, end still delivered",
			events:   []llm.TextStreamEvent{textEvent, endEvent},
			contexts: []*llm.Context{ctxWithCreatedFiles("f1")},
			respond: func([]string) ([]string, error) {
				return nil, errors.New("db down")
			},
			wantLinkCalls:  [][]string{{"f1"}},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeEnd},
		},
		{
			name:     "linker error after a partial link still announces the linked subset",
			events:   []llm.TextStreamEvent{endEvent},
			contexts: []*llm.Context{ctxWithCreatedFiles("f1", "f2")},
			respond: func([]string) ([]string, error) {
				return []string{"f1"}, errors.New("db down after first row")
			},
			wantLinkCalls:  [][]string{{"f1", "f2"}},
			wantFilesEvent: []string{"f1"},
			wantEventTypes: []llm.EventType{llm.EventTypeFiles, llm.EventTypeEnd},
		},
		{
			name:           "error stream passes through without linking",
			events:         []llm.TextStreamEvent{textEvent, {Type: llm.EventTypeError, Value: errors.New("boom")}},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1")},
			wantEventTypes: []llm.EventType{llm.EventTypeText, llm.EventTypeError},
		},
		{
			name:           "channel close without end passes through without linking",
			events:         []llm.TextStreamEvent{textEvent},
			contexts:       []*llm.Context{ctxWithCreatedFiles("f1")},
			wantEventTypes: []llm.EventType{llm.EventTypeText},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Conversations{}
			post := &model.Post{Id: "post-id", ChannelId: "channel-id", FileIds: tt.postFileIDs}
			linker := &recordingLinker{respond: tt.respond}

			decorated := c.decorateStreamWithCreatedFilesUsing(linker.link, makeEventStream(tt.events...), post, tt.extraFileIDs, tt.contexts...)
			events := drainTextStreamEvents(t, decorated)

			assert.Equal(t, tt.wantEventTypes, eventTypes(events))

			assert.Equal(t, tt.wantLinkCalls, linker.calls)
			for i := range linker.calls {
				assert.Equal(t, post.Id, linker.postIDs[i])
				assert.Equal(t, post.ChannelId, linker.channelIDs[i])
			}

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
		assert.Nil(t, c.decorateStreamWithCreatedFiles(nil, &model.Post{}, nil))
		assert.Nil(t, c.decorateStreamWithCreatedFilesUsing(nil, nil, &model.Post{}, nil))
	})

	t.Run("nil db client returns the stream unchanged", func(t *testing.T) {
		c := &Conversations{}
		stream := makeEventStream(endEvent)
		assert.Same(t, stream, c.decorateStreamWithCreatedFiles(stream, &model.Post{}, nil))
	})
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
