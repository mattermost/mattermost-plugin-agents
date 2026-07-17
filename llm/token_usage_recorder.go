// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "context"

// TokenUsageRecord is handed to the recorder once per completed LLM stream,
// after usage events have been aggregated.
type TokenUsageRecord struct {
	UserID      string     // "" when no human requesting user
	IsGuest     bool       // RequestingUser.IsGuest() snapshot; false when no human user
	IsBot       bool       // true when RequestingUser is nil or RequestingUser.IsBot
	BotUserID   string     // agent bot user ID from llm.Context; "" when unknown
	BotUsername string     // for error logging only; not persisted
	Usage       TokenUsage // aggregated over the whole stream
}

// TokenUsageRecorder receives aggregated usage. Implementations must be
// non-blocking-fast or internally bounded, must never panic, and must not
// return errors into the stream path (log-and-drop).
type TokenUsageRecorder interface {
	RecordTokenUsage(ctx context.Context, record TokenUsageRecord)
}
