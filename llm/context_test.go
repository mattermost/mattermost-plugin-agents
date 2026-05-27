// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

type contextTelemetryEvent struct {
	botName string
	event   string
	result  string
}

type fakeMCPDynamicTelemetry struct {
	events []contextTelemetryEvent
}

func (t *fakeMCPDynamicTelemetry) ObserveMCPDynamicToolEvent(botName, event, result string) {
	t.events = append(t.events, contextTelemetryEvent{botName: botName, event: event, result: result})
}

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

func TestContextObserveMCPDynamicToolEventBotLabelFallbacks(t *testing.T) {
	tests := []struct {
		name        string
		context     *Context
		wantBotName string
	}{
		{
			name:        "username",
			context:     &Context{BotUsername: "matty", BotName: "Matty"},
			wantBotName: "matty",
		},
		{
			name:        "display name",
			context:     &Context{BotName: "Matty"},
			wantBotName: "Matty",
		},
		{
			name:        "unknown",
			context:     &Context{},
			wantBotName: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			telemetry := &fakeMCPDynamicTelemetry{}
			tt.context.ToolRuntime.MCPDynamicToolTelemetry = telemetry

			tt.context.ObserveMCPDynamicToolEvent("search", "success")

			assert.Equal(t, []contextTelemetryEvent{{botName: tt.wantBotName, event: "search", result: "success"}}, telemetry.events)
		})
	}
}

func TestContextMCPDynamicSearchLoadCallSuccessState(t *testing.T) {
	c := &Context{}

	assert.False(t, c.ShouldRecordMCPDynamicSearchLoadCallSuccess("jira__get_issue"))

	c.MarkMCPDynamicToolSearch()
	assert.False(t, c.ShouldRecordMCPDynamicSearchLoadCallSuccess("jira__get_issue"))

	c.MarkMCPDynamicToolLoaded("jira__get_issue")
	assert.True(t, c.ShouldRecordMCPDynamicSearchLoadCallSuccess("jira__get_issue"))
	assert.False(t, c.ShouldRecordMCPDynamicSearchLoadCallSuccess("jira__get_issue"))
}

func TestContextRestoreMCPDynamicTools(t *testing.T) {
	var nilContext *Context
	nilContext.RestoreMCPDynamicTools([]string{"jira__get_issue"})
	nilContext.SetMCPDynamicToolRestorer(func([]string) {
		t.Fatal("nil context should not install a restorer")
	})

	c := &Context{}
	c.RestoreMCPDynamicTools([]string{"jira__get_issue"})

	var restored []string
	c.SetMCPDynamicToolRestorer(func(names []string) {
		restored = append(restored, names...)
	})

	c.RestoreMCPDynamicTools(nil)
	assert.Empty(t, restored)

	c.RestoreMCPDynamicTools([]string{"jira__get_issue"})
	assert.Equal(t, []string{"jira__get_issue"}, restored)
}
