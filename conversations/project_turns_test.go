// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

type suffixGate struct{}

func (suffixGate) CheckTurn(context.Context, *bots.Bot, *model.Channel, *model.User) error {
	return nil
}
func (suffixGate) SystemPromptSuffix(_ context.Context, bot *bots.Bot, operation string) string {
	return "project of " + bot.BotUserID() + " for " + operation
}

func TestProjectSystemPromptSuffix(t *testing.T) {
	tests := []struct {
		name  string
		botID string
		want  string
	}{
		{name: "project agent", botID: "projectbot", want: "project of projectbot for conversation"},
		{name: "regular agent", botID: "regularbot"},
		{name: "agent no longer running", botID: "goneagent"},
		{name: "no agent"},
	}
	env := setupDMTestEnv(t)
	defer env.mockAPI.AssertExpectations(t)
	env.conversations.SetProjectGate(suffixGate{})
	env.botService.SetBotsForTesting([]*bots.Bot{
		bots.NewBot(llm.BotConfig{ID: "project", BotUserID: "projectbot", ProjectChannelID: "P"}, llm.ServiceConfig{}, &model.Bot{UserId: "projectbot"}, nil),
		bots.NewBot(llm.BotConfig{ID: "regular", BotUserID: "regularbot"}, llm.ServiceConfig{}, &model.Bot{UserId: "regularbot"}, nil),
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, env.conversations.ProjectSystemPromptSuffix(context.Background(), tt.botID, llm.OperationConversation))
		})
	}
}
