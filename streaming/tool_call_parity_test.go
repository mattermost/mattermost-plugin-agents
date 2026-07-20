// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

// toolCallFieldPolicy is the parity contract for one JSON field of
// llm.ToolCall: what the live (websocket) and persisted (conversation API)
// paths must agree on. Adding a field to llm.ToolCall fails the exhaustiveness
// test until it is listed here, forcing an explicit persistence + redaction
// decision instead of silently diverging the two paths.
type toolCallFieldPolicy struct {
	// blockJSON is the ContentBlock tool_use JSON tag that persists this
	// field, or "" if not persisted on the tool_use block (Result lives on a
	// separate tool_result block).
	blockJSON string

	// visibleToNonRequester is whether the field survives redaction on BOTH
	// paths: redactToolCalls (live) and FilterForNonRequester (persisted).
	visibleToNonRequester bool
}

// toolCallFieldPolicies maps every llm.ToolCall JSON field name to its parity
// contract. Keep in lockstep with the llm.ToolCall struct — the exhaustiveness
// test enforces this.
var toolCallFieldPolicies = map[string]toolCallFieldPolicy{
	// Tool identity / metadata: persisted and visible to everyone.
	"id":                 {blockJSON: "id", visibleToNonRequester: true},
	"name":               {blockJSON: "name", visibleToNonRequester: true},
	"description":        {blockJSON: "description", visibleToNonRequester: true},
	"title":              {blockJSON: "title", visibleToNonRequester: true},
	"server_origin":      {blockJSON: "server_origin", visibleToNonRequester: true},
	"status":             {blockJSON: "status", visibleToNonRequester: true},
	"user_interaction":   {blockJSON: "user_interaction", visibleToNonRequester: true},
	"would_auto_execute": {blockJSON: "would_auto_execute", visibleToNonRequester: true},

	// Private payloads: persisted (for the requester) but redacted for others.
	"arguments":     {blockJSON: "input", visibleToNonRequester: false},
	"mcp_bare_name": {blockJSON: "mcp_bare_name", visibleToNonRequester: false},

	// Result is not persisted on the tool_use block (it lives on the paired
	// tool_result block) and is redacted from the live payload for others.
	"result": {blockJSON: "", visibleToNonRequester: false},
}

// fullyPopulatedToolCall returns a ToolCall with every field set to a non-zero
// value so the parity tests can distinguish "kept" from "cleared".
func fullyPopulatedToolCall() llm.ToolCall {
	return llm.ToolCall{
		ID:               "tc-1",
		Name:             "mattermost__create_post",
		Description:      "Create a post",
		Title:            "Create Post",
		Arguments:        json.RawMessage(`{"channel_id":"c1"}`),
		Result:           "created post p1",
		Status:           llm.ToolCallStatusSuccess,
		MCPBareName:      "create_post",
		UserInteraction:  llm.UserInteractionSelect,
		WouldAutoExecute: true,
		ServerOrigin:     "embedded://mattermost",
	}
}

func toolCallJSONFieldNames(t *testing.T) []string {
	t.Helper()
	var names []string
	typ := reflect.TypeOf(llm.ToolCall{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf("llm.ToolCall field %q has no usable json tag", typ.Field(i).Name)
		}
		names = append(names, strings.Split(tag, ",")[0])
	}
	return names
}

// TestToolCallFieldPolicyExhaustive fails when llm.ToolCall gains or loses a
// JSON field without a matching entry in toolCallFieldPolicies, forcing a
// deliberate persistence + redaction decision for every field.
func TestToolCallFieldPolicyExhaustive(t *testing.T) {
	fieldNames := toolCallJSONFieldNames(t)

	for _, name := range fieldNames {
		if _, ok := toolCallFieldPolicies[name]; !ok {
			t.Errorf("llm.ToolCall JSON field %q is missing from toolCallFieldPolicies; declare whether it is persisted and visible to non-requesters", name)
		}
	}

	known := make(map[string]bool, len(fieldNames))
	for _, name := range fieldNames {
		known[name] = true
	}
	for name := range toolCallFieldPolicies {
		if !known[name] {
			t.Errorf("toolCallFieldPolicies lists %q which is not a JSON field of llm.ToolCall", name)
		}
	}
}

// TestBuildContentBlocksPersistsPolicyFields asserts the live-path block writer
// (buildContentBlocks) persists every field the policy marks as persisted.
func TestBuildContentBlocksPersistsPolicyFields(t *testing.T) {
	acc := newTurnAccumulator("conv-id", "post-id", "", false, false)
	acc.toolCalls = []llm.ToolCall{fullyPopulatedToolCall()}

	blocks := acc.buildContentBlocks()
	require.Len(t, blocks, 1)

	blockMap := toJSONMap(t, blocks[0])
	for field, policy := range toolCallFieldPolicies {
		if policy.blockJSON == "" {
			continue
		}
		require.Falsef(t, isEmptyJSONValue(blockMap[policy.blockJSON]),
			"buildContentBlocks dropped persisted field %q (block tag %q)", field, policy.blockJSON)
	}
}

// TestToolCallRedactionParity is the core drift-guard: the live redaction
// (redactToolCalls) and the persisted redaction (FilterForNonRequester) must
// agree field-for-field with the policy table, so a non-requester sees the same
// tool identity whether the call arrives live or after reload.
func TestToolCallRedactionParity(t *testing.T) {
	call := fullyPopulatedToolCall()

	// Live path: redact the wire ToolCall.
	liveRedacted := redactToolCalls([]llm.ToolCall{call})
	require.Len(t, liveRedacted, 1)
	liveMap := toJSONMap(t, liveRedacted[0])

	// Persisted path: write a tool_use block (unshared so FilterForNonRequester
	// redacts it) using the same live-path writer, then filter it.
	acc := newTurnAccumulator("conv-id", "post-id", "", false, false) // isDM=false => Shared=false
	acc.toolCalls = []llm.ToolCall{call}
	blocks := acc.buildContentBlocks()
	persistedRedacted := conversation.FilterForNonRequester(blocks)
	require.Len(t, persistedRedacted, 1)
	blockMap := toJSONMap(t, persistedRedacted[0])

	for field, policy := range toolCallFieldPolicies {
		liveVal := liveMap[field]
		if policy.visibleToNonRequester {
			require.Falsef(t, isEmptyJSONValue(liveVal),
				"live redaction dropped visible field %q", field)
			if policy.blockJSON != "" {
				require.Falsef(t, isEmptyJSONValue(blockMap[policy.blockJSON]),
					"persisted redaction dropped visible field %q (block tag %q)", field, policy.blockJSON)
			}
			continue
		}

		require.Truef(t, isEmptyJSONValue(liveVal),
			"live redaction leaked private field %q", field)
		if policy.blockJSON != "" {
			require.Truef(t, isEmptyJSONValue(blockMap[policy.blockJSON]),
				"persisted redaction leaked private field %q (block tag %q)", field, policy.blockJSON)
		}
	}
}

func toJSONMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// isEmptyJSONValue reports whether a value decoded from JSON is the zero value
// for its type (or absent). JSON numbers decode to float64.
func isEmptyJSONValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case float64:
		return t == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}
