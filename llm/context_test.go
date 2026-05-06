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

func TestContext_CustomPromptVars(t *testing.T) {
	tests := []struct {
		name     string
		context  *Context
		expected map[string]string
	}{
		{
			name: "all fields populated",
			context: &Context{
				Time:    "Mon, 31 Mar 2026 16:00:00 UTC",
				BotName: "AI Assistant",
				RequestingUser: &model.User{
					Username:  "johndoe",
					FirstName: "John",
					LastName:  "Doe",
				},
				Channel: &model.Channel{
					Name:        "town-square",
					DisplayName: "Town Square",
				},
				Team: &model.Team{
					Name:        "engineering",
					DisplayName: "Engineering",
				},
			},
			expected: map[string]string{
				"Username":    "johndoe",
				"FirstName":   "John",
				"LastName":    "Doe",
				"Channel":     "Town Square",
				"ChannelName": "town-square",
				"Team":        "Engineering",
				"TeamName":    "engineering",
				"Time":        "Mon, 31 Mar 2026 16:00:00 UTC",
				"BotName":     "AI Assistant",
			},
		},
		{
			name: "nil optional fields",
			context: &Context{
				Time:    "Mon, 31 Mar 2026 16:00:00 UTC",
				BotName: "Bot",
			},
			expected: map[string]string{
				"Time":    "Mon, 31 Mar 2026 16:00:00 UTC",
				"BotName": "Bot",
			},
		},
		{
			name: "sensitive fields excluded",
			context: &Context{
				Time:               "now",
				BotName:            "Bot",
				BotUsername:        "bot",
				BotUserID:          "secret-id",
				BotModel:           "gpt-4",
				BotServiceType:     "openai",
				CustomInstructions: "top secret instructions",
				SiteURL:            "https://internal.example.com",
				ServerName:         "MyServer",
				CompanyName:        "Acme",
				RequestingUser: &model.User{
					Username:  "johndoe",
					Email:     "john@example.com",
					FirstName: "John",
					LastName:  "Doe",
				},
			},
			expected: map[string]string{
				"Time":      "now",
				"BotName":   "Bot",
				"Username":  "johndoe",
				"FirstName": "John",
				"LastName":  "Doe",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := tt.context.CustomPromptVars()
			assert.Equal(t, tt.expected, vars)
		})
	}
}
