// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestConvertToResponsesMessagesIgnoresStoredReasoningOnAssistantInput(t *testing.T) {
	b := &LLM{provider: schemas.OpenAI}

	messages := b.convertToResponsesMessages([]llm.Post{
		{
			Role:      llm.PostRoleBot,
			Message:   "assistant reply",
			Reasoning: "internal reasoning summary from previous turn",
		},
	})

	require.Len(t, messages, 1)
	msg := messages[0]
	require.NotNil(t, msg.Role)
	assert.Equal(t, schemas.ResponsesInputMessageRoleAssistant, *msg.Role)
	require.NotNil(t, msg.Content)
	require.NotNil(t, msg.Content.ContentStr)
	assert.Equal(t, "assistant reply", *msg.Content.ContentStr)

	// Do not include stored reasoning in assistant input messages:
	// OpenAI rejects `input[n].summary` for assistant input items.
	assert.Nil(t, msg.ResponsesReasoning)
}
