// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestContext_SetBotFields(t *testing.T) {
	c := NewContext()
	c.SetBotFields("BotDisplay", "botuser", "user-id-123", "gpt-4", "openai", "Be helpful and concise")

	assert.Equal(t, "BotDisplay", c.BotName)
	assert.Equal(t, "botuser", c.BotUsername)
	assert.Equal(t, "user-id-123", c.BotUserID)
	assert.Equal(t, "gpt-4", c.BotModel)
	assert.Equal(t, "openai", c.BotServiceType)
	assert.Equal(t, "Be helpful and concise", c.CustomInstructions)
}

func TestContext_EffectiveToolUserID(t *testing.T) {
	t.Run("human_invoker_uses_requesting_user", func(t *testing.T) {
		c := NewContext(WithAutomatedMCPInvoker(false))
		c.RequestingUser = &model.User{Id: "user-1"}
		c.BotUserID = "bot-1"
		assert.Equal(t, "user-1", c.EffectiveToolUserID())
	})

	t.Run("automated_invoker_uses_bot_when_set", func(t *testing.T) {
		c := NewContext(WithAutomatedMCPInvoker(true))
		c.RequestingUser = &model.User{Id: "user-1"}
		c.BotUserID = "bot-1"
		assert.Equal(t, "bot-1", c.EffectiveToolUserID())
	})

	t.Run("automated_invoker_falls_back_to_requesting_user_when_bot_unset", func(t *testing.T) {
		c := NewContext(WithAutomatedMCPInvoker(true))
		c.RequestingUser = &model.User{Id: "user-1"}
		c.BotUserID = ""
		assert.Equal(t, "user-1", c.EffectiveToolUserID())
	})
}
