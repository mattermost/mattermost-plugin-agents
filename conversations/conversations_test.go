// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockConfigProvider struct{}

func (m *mockConfigProvider) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func TestConversationMentionHandlingEval(t *testing.T) {
	evalConfigs := []struct {
		filename string
		rubrics  []string
	}{
		{
			filename: "attribution_long_thread.json",
			rubrics: []string{
				"is a list of bugs",
				"includes a description of each bug",
				"attributes each bug to a user",
				"attributes the bug about trying to save without a color and the save button not doing anything to @maria.nunez",
				"the bug about the end user being able to change channel banner is attributed to @maria.nunez",
			},
		},
	}

	for _, config := range evalConfigs {
		evals.Run(t, "conversation from "+config.filename, func(t *evals.EvalT) {
			threadData := evals.LoadThreadFromJSON(t, filepath.Join(".", config.filename))
			mention := threadData.LatestPost()
			botUser := threadData.BotUser()
			require.NotNil(t, botUser, "thread export has no agent user")

			response := respondAsAgent(t, evalScenario{
				bot: llm.BotConfig{
					ID:          botUser.Id,
					Name:        botUser.Username,
					DisplayName: "Matty",
					// The fixture has animated GIFs, which some providers reject, and no
					// rubric depends on them.
					EnableVision:       false,
					ChannelAccessLevel: llm.ChannelAccessLevelAll,
					UserAccessLevel:    llm.UserAccessLevelAll,
				},
				botUser:   botUser,
				team:      threadData.Team,
				channel:   threadData.Channel,
				users:     threadData.Users,
				thread:    threadData.PostList,
				fileInfos: threadData.FileInfos,
				files:     threadData.Files,
			}, mention)
			assert.NotEmpty(t, response, "Expected a non-empty conversation response")

			for _, rubric := range config.rubrics {
				evals.LLMRubricT(t, rubric, response)
			}
		})
	}
}
