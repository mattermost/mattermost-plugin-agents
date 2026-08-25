// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// recordTTL bounds how long delegation records are kept for reconciliation.
const recordTTL = 7 * 24 * time.Hour

// Record is the persisted state of a delegation, keyed both by the delegation
// conversation ID and by the parent tool call ID. It powers the reload
// reconciliation endpoint and cross-node completion events.
type Record struct {
	// DelegationID is the delegation conversation ID.
	DelegationID string `json:"delegation_id"`
	// ParentToolCallID is the ask_agent tool_use ID on the delegating agent's turn.
	ParentToolCallID string `json:"parent_tool_call_id"`

	InitiatorUserID      string `json:"initiator_user_id"`
	SourceBotID          string `json:"source_bot_id"`
	SourceBotUsername    string `json:"source_bot_username"`
	TargetBotID          string `json:"target_bot_id"`
	TargetBotUsername    string `json:"target_bot_username"`
	TargetBotDisplayName string `json:"target_bot_display_name"`

	// TaskPostID is the root post of the delegation thread in the
	// initiator's DM with the target agent.
	TaskPostID string `json:"task_post_id"`
	ChannelID  string `json:"channel_id"`

	CreatedAt int64 `json:"created_at"`
}

func recordKeyByConversation(conversationID string) string {
	return "delegation_conv_" + conversationID
}

func recordKeyByParentToolCall(parentToolCallID string) string {
	return "delegation_ptc_" + parentToolCallID
}

// saveRecord persists the record under both lookup keys. On a partial write
// (second index fails) the first key is deleted again so the two lookups can
// never disagree about the record's existence.
func saveRecord(mmClient mmapi.Client, record Record) error {
	if record.DelegationID == "" {
		return fmt.Errorf("delegation record requires a delegation ID")
	}
	if err := mmClient.KVSetWithExpiry(recordKeyByConversation(record.DelegationID), record, recordTTL); err != nil {
		return fmt.Errorf("failed to persist delegation record: %w", err)
	}
	if record.ParentToolCallID != "" {
		if err := mmClient.KVSetWithExpiry(recordKeyByParentToolCall(record.ParentToolCallID), record, recordTTL); err != nil {
			if cleanupErr := mmClient.KVDelete(recordKeyByConversation(record.DelegationID)); cleanupErr != nil {
				mmClient.LogWarn("Failed to clean up partial delegation record", "error", cleanupErr, "delegation_id", record.DelegationID)
			}
			return fmt.Errorf("failed to persist delegation record by tool call: %w", err)
		}
	}
	return nil
}

// loadRecordByConversation returns the record for a delegation conversation,
// or nil when none exists.
func loadRecordByConversation(mmClient mmapi.Client, conversationID string) (*Record, error) {
	return loadRecord(mmClient, recordKeyByConversation(conversationID))
}

// loadRecordByParentToolCall returns the record for a parent tool call ID, or
// nil when none exists.
func loadRecordByParentToolCall(mmClient mmapi.Client, parentToolCallID string) (*Record, error) {
	return loadRecord(mmClient, recordKeyByParentToolCall(parentToolCallID))
}

func loadRecord(mmClient mmapi.Client, key string) (*Record, error) {
	var record Record
	if err := mmClient.KVGet(key, &record); err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load delegation record: %w", err)
	}
	if record.DelegationID == "" {
		return nil, nil
	}
	return &record, nil
}
