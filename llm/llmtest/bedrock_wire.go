// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmtest

import (
	"encoding/json"
	"fmt"
	"strings"
)

type bedrockWireRequest struct {
	Messages []bedrockWireMessage `json:"messages"`
}

type bedrockWireMessage struct {
	Role    string             `json:"role"`
	Content []bedrockWireBlock `json:"content"`
}

type bedrockWireBlock struct {
	Text             *string                `json:"text"`
	ToolUse          *bedrockWireToolUse    `json:"toolUse"`
	ToolResult       *bedrockWireToolResult `json:"toolResult"`
	ReasoningContent *bedrockWireReasoning  `json:"reasoningContent"`
}

type bedrockWireToolUse struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
}

type bedrockWireToolResult struct {
	ToolUseID string             `json:"toolUseId"`
	Content   []bedrockWireBlock `json:"content"`
}

type bedrockWireReasoning struct {
	ReasoningText *struct {
		Text      *string `json:"text"`
		Signature *string `json:"signature"`
	} `json:"reasoningText"`
}

// BedrockConverseViolations checks a Bedrock Converse request body against
// the validation rules that make Converse (serving a Claude model) answer
// ValidationException: messages that do not start with a user message or do
// not alternate between user and assistant, empty content lists, blank text
// blocks, reasoning without a signature, nameless toolUse blocks, a toolUse
// without a matching toolResult in the next message, and a toolResult without
// a matching toolUse in the previous message. It returns one description per
// violation.
func BedrockConverseViolations(body []byte) []string {
	var req bedrockWireRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return []string{fmt.Sprintf("request body is not valid JSON: %v", err)}
	}

	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	if len(req.Messages) == 0 {
		report("messages must be non-empty")
	} else if req.Messages[0].Role != "user" {
		report("messages.0: conversation must start with a user message, got %q", req.Messages[0].Role)
	}

	for i, msg := range req.Messages {
		where := fmt.Sprintf("messages.%d (%s)", i, msg.Role)
		if msg.Role != "user" && msg.Role != "assistant" {
			report("%s: role must be user or assistant", where)
		}
		if i > 0 && msg.Role == req.Messages[i-1].Role {
			report("%s: roles must alternate between user and assistant", where)
		}
		if len(msg.Content) == 0 {
			report("%s: content must be non-empty", where)
		}
		checkBedrockBlocks(msg.Content, where, report)

		for _, block := range msg.Content {
			if use := block.ToolUse; use != nil {
				if use.ToolUseID == "" || use.Name == "" {
					report("%s: toolUse must have a toolUseId and a name (toolUseId=%q name=%q)", where, use.ToolUseID, use.Name)
				}
				if msg.Role != "assistant" || i+1 >= len(req.Messages) || !hasBedrockToolResult(req.Messages[i+1].Content, use.ToolUseID) {
					report("%s: toolUse %q has no toolResult in the next message", where, use.ToolUseID)
				}
			}
			if result := block.ToolResult; result != nil {
				if msg.Role != "user" || i == 0 || !hasBedrockToolUse(req.Messages[i-1].Content, result.ToolUseID) {
					report("%s: toolResult %q has no toolUse in the previous message", where, result.ToolUseID)
				}
			}
		}
	}

	return violations
}

// checkBedrockBlocks reports blank text blocks and unsigned reasoning in
// blocks, including the content of tool results.
func checkBedrockBlocks(blocks []bedrockWireBlock, where string, report func(string, ...any)) {
	for j, block := range blocks {
		blockWhere := fmt.Sprintf("%s.content.%d", where, j)
		if block.Text != nil && strings.TrimSpace(*block.Text) == "" {
			report("%s: text content blocks must be non-empty", blockWhere)
		}
		if block.ReasoningContent != nil {
			text := block.ReasoningContent.ReasoningText
			if text == nil || text.Signature == nil || *text.Signature == "" {
				report("%s: reasoningContent must carry a signature", blockWhere)
			}
		}
		if block.ToolResult != nil {
			checkBedrockBlocks(block.ToolResult.Content, blockWhere+" (toolResult)", report)
		}
	}
}

func hasBedrockToolUse(blocks []bedrockWireBlock, id string) bool {
	for _, b := range blocks {
		if b.ToolUse != nil && b.ToolUse.ToolUseID == id {
			return true
		}
	}
	return false
}

func hasBedrockToolResult(blocks []bedrockWireBlock, id string) bool {
	for _, b := range blocks {
		if b.ToolResult != nil && b.ToolResult.ToolUseID == id {
			return true
		}
	}
	return false
}
