// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

// Well-formed 26-char IDs: the agent access gate denies IDs no policy can be
// evaluated against, so the DM would never reach the LLM.
const (
	dmEvalBotUserID = "mattybotid1234567890123abc"
	dmEvalUserID    = "coreyuserid1234567890123ab"
	dmEvalPostID    = "dmevalpostid1234567890123a"
	dmEvalTeamID    = "dmevalteamid1234567890123a"
	dmEvalChannelID = "dmevalchannelid1234567890a"
)

func TestDirectMessageConversationsEval(t *testing.T) {
	webSearch := llm.Tool{
		Name:        "WebSearch",
		Description: "Search the web for information.",
		Schema:      llm.NewJSONSchemaFromStruct[struct{ Term string }](),
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			return "No results found.", nil
		},
	}

	evalConfigs := []struct {
		name    string
		message string
		rubrics []string
	}{
		{
			name:    "knows who it is",
			message: "Hi, who are you?",
			rubrics: []string{
				"explanation that they are Matty or @matty an AI agent",
			},
		},
		{
			name:    "knows the model powering it",
			message: "What model are you using?",
			rubrics: []string{
				"explains they are powered by " + evalServiceModel,
			},
		},
		{
			name:    "knows it doesn't know what it can do",
			message: "Can you access posts in private channels?",
			rubrics: []string{
				"refers the user to the documentation",
			},
		},
		{
			name:    "tool introspection",
			message: "What tools do you have?",
			rubrics: []string{
				"mentions web search as a tool it can use",
				"refers the user to the documentation",
			},
		},
	}

	for _, config := range evalConfigs {
		evals.Run(t, "bot dm "+config.name, func(t *evals.EvalT) {
			botUser := &model.User{Id: dmEvalBotUserID, Username: "matty", FirstName: "Matty", IsBot: true}
			user := &model.User{Id: dmEvalUserID, Username: "corey", Locale: "en"}
			channel := &model.Channel{
				Id:     dmEvalChannelID,
				TeamId: dmEvalTeamID,
				Type:   model.ChannelTypeDirect,
				Name:   model.GetDMNameFromIds(dmEvalUserID, dmEvalBotUserID),
			}
			post := &model.Post{
				Id:        dmEvalPostID,
				UserId:    dmEvalUserID,
				ChannelId: channel.Id,
				Message:   config.message,
				CreateAt:  model.GetMillis(),
			}
			thread := model.NewPostList()
			thread.AddPost(post)
			thread.AddOrder(post.Id)

			response := respondAsAgent(t, evalScenario{
				bot: llm.BotConfig{
					ID:                 dmEvalBotUserID,
					Name:               "matty",
					DisplayName:        "Matty",
					ChannelAccessLevel: llm.ChannelAccessLevelAll,
					UserAccessLevel:    llm.UserAccessLevelAll,
				},
				botUser: botUser,
				team:    &model.Team{Id: dmEvalTeamID, Name: "fastfutures", DisplayName: "Fast Futures"},
				channel: channel,
				users:   map[string]*model.User{user.Id: user},
				thread:  thread,
				tools:   []llm.Tool{webSearch},
			}, post)
			assert.NotEmpty(t, response, "Expected a non-empty DM response")

			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, response)
			}
		})
	}
}
