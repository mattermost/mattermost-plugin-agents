// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"io"
	"slices"
	"strings"
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

	// ServerTools records provider-executed tool activity that produced this
	// assistant turn (web search, web fetch, code execution). The provider
	// puts its own results in the model's context during the request that ran
	// them, but they are gone from every later request — so without replaying
	// them the model forgets work it just did and repeats it. Replayed as a
	// labeled record rather than reconstructed provider blocks: the fields are
	// display-truncated, and the sandbox container is not carried forward, so
	// forged result blocks would point at a container that no longer exists.
	ServerTools []ServerToolUse

	// AssistantSegments records where response text and server-tool activity
	// occurred relative to each other. ServerTools holds each invocation's
	// final payload; server-tool segments reference that snapshot by ID.
	AssistantSegments []TurnSegment
}

type CompletionRequest struct {
	Posts            []Post
	Context          *Context
	Operation        string
	OperationSubType string
}

func (b *CompletionRequest) Truncate(maxTokens int, countTokens func(string) int) bool {
	oldPosts := b.Posts
	b.Posts = make([]Post, 0, len(oldPosts))
	var totalTokens int
	for i := len(oldPosts) - 1; i >= 0; i-- {
		post := oldPosts[i]
		if totalTokens >= maxTokens {
			slices.Reverse(b.Posts)
			return true
		}
		postTokens := countTokens(post.Message)
		if (totalTokens + postTokens) > maxTokens {
			charactersToCut := (postTokens - (maxTokens - totalTokens)) * 4
			post.Message = strings.TrimSpace(post.Message[charactersToCut:])
			// The partial-message cut cannot be mapped safely onto interleaved
			// activity segments. Drop that old turn's replay metadata rather
			// than bypassing truncation by sending the original segment text.
			post.AssistantSegments = nil
			post.ServerTools = nil
			b.Posts = append(b.Posts, post)
			slices.Reverse(b.Posts)
			return true
		}
		totalTokens += postTokens
		b.Posts = append(b.Posts, post)
	}

	slices.Reverse(b.Posts)
	return false
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
