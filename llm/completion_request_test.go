// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestCompletionRequestTruncate(t *testing.T) {
	// One token per four bytes keeps the expected cuts easy to derive.
	countTokens := func(text string) int { return len(text) / 4 }
	chars := func(n int) string { return strings.Repeat("a", n) }
	note := Post{Role: PostRoleUser, Message: TruncatedHistoryNote}
	noteTokens := countTokens(TruncatedHistoryNote)
	prefixTokens := countTokens(TruncatedMessagePrefix)
	// tailChars is the longest suffix that fits in the tokens left after the
	// system prompt, the note, the kept posts, and the cut marker.
	tailChars := func(maxTokens, usedTokens int) int {
		return (maxTokens-usedTokens-noteTokens-prefixTokens)*4 + 3
	}

	toolPost := func(result string) Post {
		return Post{Role: PostRoleBot, ToolUse: []ToolCall{{ID: "tc1", Name: "search", Arguments: json.RawMessage(`{}`), Result: result}}}
	}

	tests := []struct {
		name          string
		posts         []Post
		maxTokens     int
		wantTruncated bool
		want          []Post
	}{
		{
			name: "fits without changes",
			posts: []Post{
				{Role: PostRoleSystem, Message: "You are a helpful assistant."},
				{Role: PostRoleUser, Message: "Hello, how are you?"},
			},
			maxTokens: 50,
			want: []Post{
				{Role: PostRoleSystem, Message: "You are a helpful assistant."},
				{Role: PostRoleUser, Message: "Hello, how are you?"},
			},
		},
		{
			name: "cuts the oldest kept message and marks it without a note",
			posts: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				{Role: PostRoleUser, Message: chars(400)},
				{Role: PostRoleBot, Message: chars(400)},
				{Role: PostRoleUser, Message: chars(400)},
			},
			maxTokens:     250,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				{Role: PostRoleUser, Message: TruncatedMessagePrefix + chars(tailChars(250, 10+200))},
				{Role: PostRoleBot, Message: chars(400)},
				{Role: PostRoleUser, Message: chars(400)},
			},
		},
		{
			name: "drops older posts and adds a note after the system prompt",
			posts: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				{Role: PostRoleUser, Message: "oldest " + chars(400)},
				{Role: PostRoleBot, Message: chars(400)},
				{Role: PostRoleUser, Message: chars(400)},
				{Role: PostRoleBot, Message: chars(400)},
			},
			maxTokens:     250,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				note,
				{Role: PostRoleBot, Message: TruncatedMessagePrefix + chars(tailChars(250, 10+200))},
				{Role: PostRoleUser, Message: chars(400)},
				{Role: PostRoleBot, Message: chars(400)},
			},
		},
		{
			name: "never drops or cuts system posts, even over budget, and keeps the newest post whole",
			posts: []Post{
				{Role: PostRoleSystem, Message: chars(2000)},
				{Role: PostRoleUser, Message: chars(400)},
				{Role: PostRoleUser, Message: chars(400)},
			},
			maxTokens:     300,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleSystem, Message: chars(2000)},
				note,
				{Role: PostRoleUser, Message: chars(400)},
			},
		},
		{
			name: "keeps every system post and puts the note after the leading ones",
			posts: []Post{
				{Role: PostRoleSystem, Message: "first"},
				{Role: PostRoleSystem, Message: "second"},
				{Role: PostRoleUser, Message: "oldest"},
				{Role: PostRoleUser, Message: chars(4000)},
				{Role: PostRoleSystem, Message: "retry limit"},
				{Role: PostRoleUser, Message: chars(400)},
			},
			maxTokens:     200,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleSystem, Message: "first"},
				{Role: PostRoleSystem, Message: "second"},
				note,
				{Role: PostRoleUser, Message: TruncatedMessagePrefix + chars(tailChars(200, 4+100))},
				{Role: PostRoleSystem, Message: "retry limit"},
				{Role: PostRoleUser, Message: chars(400)},
			},
		},
		{
			name: "counts tool results and drops a tool post whole instead of cutting it",
			posts: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				{Role: PostRoleUser, Message: chars(40)},
				toolPost(chars(2000)),
				{Role: PostRoleUser, Message: chars(40)},
			},
			maxTokens:     200,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				note,
				{Role: PostRoleUser, Message: chars(40)},
			},
		},
		{
			name: "keeps the newest post whole when it carries tool calls",
			posts: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				{Role: PostRoleUser, Message: chars(40)},
				toolPost(chars(2000)),
			},
			maxTokens:     200,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				note,
				toolPost(chars(2000)),
			},
		},
		{
			name: "does not add a second note",
			posts: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				note,
				{Role: PostRoleUser, Message: "oldest"},
				{Role: PostRoleUser, Message: chars(4000)},
				{Role: PostRoleUser, Message: chars(400)},
			},
			maxTokens:     200,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleSystem, Message: chars(40)},
				note,
				{Role: PostRoleUser, Message: TruncatedMessagePrefix + chars(tailChars(200, 10+100))},
				{Role: PostRoleUser, Message: chars(400)},
			},
		},
		{
			// 115 bytes fit; the cut moves forward to the next rune start.
			name: "cuts on a rune boundary",
			posts: []Post{
				{Role: PostRoleUser, Message: strings.Repeat("é", 400)},
				{Role: PostRoleUser, Message: chars(400)},
			},
			maxTokens:     150,
			wantTruncated: true,
			want: []Post{
				{Role: PostRoleUser, Message: TruncatedMessagePrefix + strings.Repeat("é", 57)},
				{Role: PostRoleUser, Message: chars(400)},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.posts)
			req := CompletionRequest{Posts: tc.posts}

			assert.Equal(t, tc.wantTruncated, req.Truncate(tc.maxTokens, countTokens))
			assert.Equal(t, tc.want, req.Posts)
			assert.Equal(t, original, tc.posts, "the caller's posts must not be modified")
			for _, post := range req.Posts {
				assert.True(t, utf8.ValidString(post.Message))
			}
		})
	}
}
