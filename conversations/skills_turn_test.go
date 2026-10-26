// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/skills"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type skillsTestPromptStore struct {
	prompts []customprompts.CustomPrompt
}

func (s skillsTestPromptStore) ListForUser(userID string, _ bool) ([]customprompts.CustomPrompt, error) {
	var out []customprompts.CustomPrompt
	for _, p := range s.prompts {
		if p.CreatorID == userID {
			out = append(out, p)
		}
	}
	return out, nil
}

// Skills reach the system prompt and the tool store only through the turn
// policy, and only for ordinary conversations.
func TestTurnPolicySkills(t *testing.T) {
	const loadInstruction = "call load_skill"
	personal := customprompts.CustomPrompt{ID: model.NewId(), CreatorID: testRequesterID, Name: "Standup", Description: "Use for standup notes.", Template: "Standup"}

	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	tests := []struct {
		name                string
		channelType         model.ChannelType
		allowToolsInChannel bool
		operation           string
		wantSkills          []string
	}{
		{name: "new DM conversation", channelType: model.ChannelTypeDirect, wantSkills: []string{"mattermost/summarize-conversations", personal.ID}},
		{name: "DM conversation follow-up", channelType: model.ChannelTypeDirect, operation: llm.OperationConversation, wantSkills: []string{"mattermost/summarize-conversations", personal.ID}},
		{name: "channel mention lists no personal skills", channelType: model.ChannelTypeOpen, allowToolsInChannel: true, wantSkills: []string{"mattermost/summarize-conversations"}},
		{name: "channel mention with tool calling disabled", channelType: model.ChannelTypeOpen},
		{name: "search conversation", channelType: model.ChannelTypeDirect, operation: llm.OperationSearch},
		{name: "thread analysis conversation", channelType: model.ChannelTypeDirect, operation: llm.OperationThreadAnalysis},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newChannelFollowUpTestBuilder(t, []llm.Tool{
				channelFollowUpTestMCPTool("mattermost__read_channel", "embedded://mattermost", "Read channel posts"),
			}, &channelFollowUpTestConfig{enableChannelMentionToolCalling: tt.allowToolsInChannel})
			builder.SetSkillProvider(skills.NewProvider(skillsTestPromptStore{prompts: []customprompts.CustomPrompt{personal}}, promptsEngine, func() bool { return true }))

			cfg := channelFollowUpTestBot().GetConfig()
			cfg.Skills = llm.SkillsConfig{AllowPersonal: true}
			bot := bots.NewBot(cfg, llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI}, &model.Bot{UserId: "bot-id", Username: "matty"}, nil)

			var conv *store.Conversation
			if tt.operation != "" {
				conv = &store.Conversation{ID: "conv-id", Operation: tt.operation}
			}
			isDM := tt.channelType == model.ChannelTypeDirect
			policy := &turnPolicy{
				c:            &Conversations{contextBuilder: builder, toolPolicyChecker: mapPolicyChecker{}},
				bot:          bot,
				user:         &model.User{Id: testRequesterID, Username: "user", Locale: "en"},
				channel:      &model.Channel{Id: "channel-id", TeamId: "team-id", Type: tt.channelType},
				conv:         conv,
				isDM:         isDM,
				toolsEnabled: isDM || tt.allowToolsInChannel,
			}
			llmContext := policy.buildContext(context.Background(), "")

			systemPrompt, err := promptsEngine.Format(prompts.PromptDirectMessageQuestionSystem, llmContext)
			require.NoError(t, err)

			if len(tt.wantSkills) == 0 {
				assert.False(t, llmContext.IsToolCallable(skills.LoadSkillToolName))
				assert.NotContains(t, systemPrompt, loadInstruction)
				return
			}
			require.True(t, llmContext.IsToolCallable(skills.LoadSkillToolName))
			require.True(t, llmContext.IsToolCallable(skills.RefreshSkillsToolName))
			assert.True(t, policy.autoExecute(llmContext)(llm.ToolCall{Name: skills.LoadSkillToolName}))
			assert.Contains(t, systemPrompt, loadInstruction)
			assert.Len(t, llmContext.Skills, len(tt.wantSkills))
			for _, id := range tt.wantSkills {
				assert.Contains(t, systemPrompt, "- id: "+id+" | ")
			}
		})
	}
}
