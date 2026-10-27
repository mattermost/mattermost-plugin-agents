// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

type File struct {
	MimeType string
	Size     int64
	Data     []byte
	Reader   io.Reader
}

func IsSupportedImageMimeType(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

type PostRole int

const (
	PostRoleUser PostRole = iota
	PostRoleBot
	PostRoleSystem
)

type Post struct {
	Role               PostRole
	Message            string
	Files              []File
	ToolUse            []ToolCall
	Reasoning          string // Extended thinking/reasoning content from models that support it
	ReasoningSignature string // Signature for thinking blocks (opaque verification field)

	// ServerTools is provider-executed activity from this turn. The provider
	// drops those results from later requests, so we replay a labeled summary
	// (not reconstructed blocks: fields are truncated and the sandbox is gone).
	ServerTools []ServerToolUse

	// AssistantSegments is arrival order of text vs server-tool activity.
	// ServerTools holds payloads; segments reference them by ID.
	AssistantSegments []TurnSegment
}

type CompletionRequest struct {
	Posts            []Post
	Context          *Context
	Operation        string
	OperationSubType string

	// RestrictFallbacks, when true, attaches only AllowedFallbackServiceIDs
	// (already truncated at the first denied hop). When false, the model uses
	// its full configured chain (evals, tests, non-user jobs).
	RestrictFallbacks         bool
	AllowedFallbackServiceIDs []string
}

// TruncatedHistoryNote tells the model that older messages were dropped to
// fit the context window.
const TruncatedHistoryNote = "[Earlier messages in this conversation were omitted to fit the context window.]"

// TruncatedMessagePrefix marks a message whose beginning was cut to fit the
// context window.
const TruncatedMessagePrefix = "[…truncated] "

// postTokens estimates a post's cost, including tool-call arguments and
// results, which providers replay alongside the message text.
func postTokens(post Post, countTokens func(string) int) int {
	tokens := countTokens(post.Message)
	for _, tc := range post.ToolUse {
		tokens += countTokens(string(tc.Arguments)) + countTokens(tc.Result)
	}
	return tokens
}

func isTruncatedHistoryNote(post Post) bool {
	return post.Role == PostRoleUser && post.Message == TruncatedHistoryNote && len(post.ToolUse) == 0
}

// withTruncatedHistoryNote inserts the note right after the leading system
// posts, as a user post so the history never starts with an assistant turn.
func withTruncatedHistoryNote(posts []Post) []Post {
	insertAt := len(posts)
	for i, post := range posts {
		if isTruncatedHistoryNote(post) {
			return posts
		}
		if post.Role != PostRoleSystem && insertAt == len(posts) {
			insertAt = i
		}
	}
	return slices.Insert(posts, insertAt, Post{Role: PostRoleUser, Message: TruncatedHistoryNote})
}

// tailWithinTokens returns the longest suffix of text, starting on a rune
// boundary, whose estimate fits within maxTokens.
func tailWithinTokens(text string, maxTokens int, countTokens func(string) int) string {
	if maxTokens <= 0 {
		return ""
	}
	lo, hi := 0, len(text)
	for lo < hi {
		mid := (lo + hi) / 2
		if countTokens(text[mid:]) <= maxTokens {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	for lo < len(text) && !utf8.RuneStart(text[lo]) {
		lo++
	}
	return strings.TrimSpace(text[lo:])
}

// Truncate fits the request within maxTokens by dropping the oldest
// non-system posts, cutting the front of the oldest kept one when it has no
// tool calls. System posts are never dropped or cut, the newest non-system
// post is always kept with some of its text (whole when none would fit), and
// a TruncatedHistoryNote replaces dropped posts.
// Posts is replaced, never modified in place, so callers' slices are safe.
func (b *CompletionRequest) Truncate(maxTokens int, countTokens func(string) int) bool {
	total, systemTokens := 0, 0
	for _, post := range b.Posts {
		tokens := postTokens(post, countTokens)
		total += tokens
		if post.Role == PostRoleSystem {
			systemTokens += tokens
		}
	}
	if total <= maxTokens {
		return false
	}

	available := maxTokens - systemTokens - countTokens(TruncatedHistoryNote)
	posts := slices.Clone(b.Posts)
	keep := make([]bool, len(posts))
	newest, done, cut := true, false, false
	for i := len(posts) - 1; i >= 0; i-- {
		post := posts[i]
		if post.Role == PostRoleSystem || isTruncatedHistoryNote(post) {
			keep[i] = true
			continue
		}
		if done {
			continue
		}
		tokens := postTokens(post, countTokens)
		if tokens <= available {
			keep[i] = true
			available -= tokens
			newest = false
			continue
		}
		done = true
		tail := ""
		if len(post.ToolUse) == 0 {
			tail = tailWithinTokens(post.Message, available-countTokens(TruncatedMessagePrefix), countTokens)
		}
		if tail == "" {
			// Cutting would break tool call/result pairing or leave nothing
			// of the message, so the newest post stays whole.
			keep[i] = newest
			continue
		}
		keep[i] = true
		cut = true
		posts[i].Message = strings.TrimSpace(TruncatedMessagePrefix + tail)
		// Drop replay metadata: a token-budget cut cannot be mapped onto interleaved segments.
		posts[i].AssistantSegments = nil
		posts[i].ServerTools = nil
	}

	kept := make([]Post, 0, len(posts)+1)
	dropped := false
	for i, post := range posts {
		if keep[i] {
			kept = append(kept, post)
		} else {
			dropped = true
		}
	}
	if dropped {
		kept = withTruncatedHistoryNote(kept)
	}
	b.Posts = kept
	return dropped || cut
}

// ExtractSystemMessage extracts the system message from the conversation.
func (b CompletionRequest) ExtractSystemMessage() string {
	for _, post := range b.Posts {
		if post.Role == PostRoleSystem {
			return post.Message
		}
	}
	return ""
}

func (b CompletionRequest) String() string {
	// Create a string of all the posts with their role and message
	var result strings.Builder
	result.WriteString("--- Conversation ---")
	for _, post := range b.Posts {
		switch post.Role {
		case PostRoleUser:
			result.WriteString("\n--- User ---\n")
		case PostRoleBot:
			result.WriteString("\n--- Bot ---\n")
		case PostRoleSystem:
			result.WriteString("\n--- System ---\n")
		default:
			result.WriteString("\n--- <Unknown> ---\n")
		}
		result.WriteString(post.Message)
	}
	result.WriteString("\n--- Context ---\n")
	result.WriteString(b.Context.String())

	return result.String()
}
