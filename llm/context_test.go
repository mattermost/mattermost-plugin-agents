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

func TestContextResponseAttachmentBudget(t *testing.T) {
	tests := []struct {
		name      string
		configure func(c *Context)
		wantSlots int
	}{
		{
			name:      "unset budget defaults to the full per-post limit",
			configure: func(*Context) {},
			wantSlots: MaxPostAttachments,
		},
		{
			name:      "positive budget is returned as-is",
			configure: func(c *Context) { c.SetResponseAttachmentBudget(3) },
			wantSlots: 3,
		},
		{
			name:      "zero budget means no slots",
			configure: func(c *Context) { c.SetResponseAttachmentBudget(0) },
			wantSlots: 0,
		},
		{
			name:      "negative budget means no slots",
			configure: func(c *Context) { c.SetResponseAttachmentBudget(-4) },
			wantSlots: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			tt.configure(c)
			assert.Equal(t, tt.wantSlots, c.ResponseAttachmentSlots())
		})
	}

	t.Run("nil receivers are safe", func(t *testing.T) {
		var c *Context
		c.SetResponseAttachmentBudget(5)
		assert.Equal(t, 0, c.ResponseAttachmentSlots())
		var tr *ToolRuntimeContext
		tr.SetResponseAttachmentBudget(5)
		assert.Equal(t, 0, tr.ResponseAttachmentSlots())
	})
}

func TestContextCreatedFiles(t *testing.T) {
	tests := []struct {
		name  string
		files []CreatedFile
		want  []CreatedFile
	}{
		{
			name:  "no files added",
			files: nil,
			want:  nil,
		},
		{
			name:  "single file",
			files: []CreatedFile{{ID: "file1", Name: "report.md"}},
			want:  []CreatedFile{{ID: "file1", Name: "report.md"}},
		},
		{
			name: "order preserved",
			files: []CreatedFile{
				{ID: "file1", Name: "a.txt"},
				{ID: "file2", Name: "b.txt"},
				{ID: "file3", Name: "c.txt"},
			},
			want: []CreatedFile{
				{ID: "file1", Name: "a.txt"},
				{ID: "file2", Name: "b.txt"},
				{ID: "file3", Name: "c.txt"},
			},
		},
		{
			name: "empty ID skipped",
			files: []CreatedFile{
				{ID: "", Name: "no-id.txt"},
				{ID: "file1", Name: "a.txt"},
			},
			want: []CreatedFile{{ID: "file1", Name: "a.txt"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			for _, f := range tt.files {
				c.AddCreatedFile(f)
			}
			assert.Equal(t, tt.want, c.CreatedFilesList())
		})
	}
}

func TestContextCreatedFilesNilReceiver(t *testing.T) {
	var c *Context
	c.AddCreatedFile(CreatedFile{ID: "file1", Name: "a.txt"})
	assert.Nil(t, c.CreatedFilesList())

	var rt *ToolRuntimeContext
	rt.AddCreatedFile(CreatedFile{ID: "file1", Name: "a.txt"})
	assert.Nil(t, rt.CreatedFilesList())
}

func TestContextSandboxFileIDs(t *testing.T) {
	c := &Context{}

	assert.False(t, c.IsSandboxFileID("file_1"), "unknown ids are rejected")

	c.AddSandboxFileIDs("file_1", "", "file_2")
	assert.True(t, c.IsSandboxFileID("file_1"))
	assert.True(t, c.IsSandboxFileID("file_2"))
	assert.False(t, c.IsSandboxFileID(""), "empty ids are never registered")
	assert.False(t, c.IsSandboxFileID("file_3"))

	// Nil receivers are safe: recording is a no-op and lookups reject.
	var nilCtx *Context
	nilCtx.AddSandboxFileIDs("file_1")
	assert.False(t, nilCtx.IsSandboxFileID("file_1"))

	var nilRuntime *ToolRuntimeContext
	nilRuntime.AddSandboxFileIDs("file_1")
	assert.False(t, nilRuntime.IsSandboxFileID("file_1"))
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
