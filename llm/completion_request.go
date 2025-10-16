// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

type File struct {
	MimeType string
	Size     int64
	Reader   io.Reader
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
}

type CompletionRequest struct {
	Posts   []Post
	Context *Context
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
	result.WriteString(fmt.Sprintf("%+v\n", b.Context))

	return result.String()
}
