// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmtest

import (
	"encoding/json"
	"fmt"
	"strings"
)

type anthropicWireRequest struct {
	System   json.RawMessage        `json:"system"`
	Messages []anthropicWireMessage `json:"messages"`
}

type anthropicWireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicWireBlock struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// AnthropicRequestViolations checks an Anthropic Messages API request body
// against the validation rules that make Anthropic answer 400
// invalid_request_error: blank text (string content or text blocks), empty
// content lists, nameless tool_use blocks, a tool_use without a matching
// tool_result in the next message, and a tool_result without a matching
// tool_use in the previous message. It returns one description per violation.
func AnthropicRequestViolations(body []byte) []string {
	var req anthropicWireRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return []string{fmt.Sprintf("request body is not valid JSON: %v", err)}
	}

	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	if len(req.System) > 0 && string(req.System) != "null" {
		if _, err := checkAnthropicContent(req.System, "system", report); err != nil {
			report("system: %v", err)
		}
	}

	blocksByMessage := make([][]anthropicWireBlock, len(req.Messages))
	for i, msg := range req.Messages {
		where := fmt.Sprintf("messages.%d (%s)", i, msg.Role)
		blocks, err := checkAnthropicContent(msg.Content, where, report)
		if err != nil {
			report("%s: %v", where, err)
		}
		blocksByMessage[i] = blocks
	}

	for i, msg := range req.Messages {
		for _, block := range blocksByMessage[i] {
			switch block.Type {
			case "tool_use":
				if block.ID == "" || block.Name == "" {
					report("messages.%d: tool_use must have an id and a name (id=%q name=%q)", i, block.ID, block.Name)
				}
				if msg.Role != "assistant" || i+1 >= len(req.Messages) || !hasBlock(blocksByMessage[i+1], "tool_result", block.ID) {
					report("messages.%d: tool_use %q has no tool_result in the next message", i, block.ID)
				}
			case "tool_result":
				if msg.Role != "user" || i == 0 || !hasBlock(blocksByMessage[i-1], "tool_use", block.ToolUseID) {
					report("messages.%d: tool_result %q has no tool_use in the previous message", i, block.ToolUseID)
				}
			}
		}
	}

	return violations
}

// checkAnthropicContent validates a string-or-blocks content value and returns
// the decoded blocks (nil for string content).
func checkAnthropicContent(raw json.RawMessage, where string, report func(string, ...any)) ([]anthropicWireBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		report("%s: content is missing", where)
		return nil, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			report("%s: text content must be non-empty", where)
		}
		return nil, nil
	}

	var blocks []anthropicWireBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		report("%s: content must be non-empty", where)
	}
	for j, block := range blocks {
		blockWhere := fmt.Sprintf("%s.content.%d (%s)", where, j, block.Type)
		if block.Type == "text" && (block.Text == nil || strings.TrimSpace(*block.Text) == "") {
			report("%s: text content blocks must be non-empty", blockWhere)
		}
		if block.Type == "tool_result" && len(block.Content) > 0 && string(block.Content) != "null" {
			if _, err := checkAnthropicContent(block.Content, blockWhere, report); err != nil {
				report("%s: %v", blockWhere, err)
			}
		}
	}
	return blocks, nil
}

func hasBlock(blocks []anthropicWireBlock, blockType, id string) bool {
	for _, b := range blocks {
		if b.Type != blockType {
			continue
		}
		if (blockType == "tool_use" && b.ID == id) || (blockType == "tool_result" && b.ToolUseID == id) {
			return true
		}
	}
	return false
}
