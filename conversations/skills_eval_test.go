// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

const (
	skillEvalMarker    = "Filed via the incident-update skill."
	skillEvalChannelID = "townsquarechannelid12345ab"
)

var incidentUpdateSkill = customprompts.CustomPrompt{
	ID:          "incidentskillid1234567890a",
	CreatorID:   dmEvalUserID,
	Name:        "Incident update",
	Description: "Use when the user asks for an incident status update.",
	Template:    "Write the update as three lines: 'STATUS:' followed by one word, 'IMPACT:' followed by one sentence, and 'NEXT:' followed by one sentence. Then end with this exact line: " + skillEvalMarker,
}

var haikuSkill = customprompts.CustomPrompt{
	ID:          "haikuskillid12345678901234",
	CreatorID:   dmEvalUserID,
	Name:        "Haiku",
	Description: "Use when the user asks for a poem.",
	Template:    "Answer as a haiku.",
}

// evalSkillStore lists listed until listCalls calls have been made, then
// listed plus added, as if the user created a skill mid-conversation.
type evalSkillStore struct {
	listed    []customprompts.CustomPrompt
	added     []customprompts.CustomPrompt
	listCalls int32
	calls     atomic.Int32
}

func (s *evalSkillStore) ListForUser(string, bool) ([]customprompts.CustomPrompt, error) {
	if s.calls.Add(1) > s.listCalls {
		return append(append([]customprompts.CustomPrompt{}, s.listed...), s.added...), nil
	}
	return s.listed, nil
}

func dmSkillScenario(store *evalSkillStore, tools []llm.Tool, message string) (evalScenario, *model.Post) {
	botUser := &model.User{Id: dmEvalBotUserID, Username: "matty", FirstName: "Matty", IsBot: true}
	user := &model.User{Id: dmEvalUserID, Username: "corey", Locale: "en"}
	channel := &model.Channel{
		Id:     dmEvalChannelID,
		TeamId: dmEvalTeamID,
		Type:   model.ChannelTypeDirect,
		Name:   model.GetDMNameFromIds(dmEvalUserID, dmEvalBotUserID),
	}
	post := &model.Post{Id: dmEvalPostID, UserId: dmEvalUserID, ChannelId: channel.Id, Message: message, CreateAt: model.GetMillis()}
	thread := model.NewPostList()
	thread.AddPost(post)
	thread.AddOrder(post.Id)

	return evalScenario{
		bot: llm.BotConfig{
			ID:                 dmEvalBotUserID,
			Name:               "matty",
			DisplayName:        "Matty",
			ChannelAccessLevel: llm.ChannelAccessLevelAll,
			UserAccessLevel:    llm.UserAccessLevelAll,
			Skills:             llm.SkillsConfig{AllowPersonal: true},
		},
		botUser:    botUser,
		team:       &model.Team{Id: dmEvalTeamID, Name: "fastfutures", DisplayName: "Fast Futures"},
		channel:    channel,
		users:      map[string]*model.User{user.Id: user},
		thread:     thread,
		tools:      tools,
		skillStore: store,
	}, post
}

func TestSkillsEval(t *testing.T) {
	evalConfigs := []struct {
		name        string
		store       func() *evalSkillStore
		message     string
		wantMarker  bool
		wantRubrics []string
	}{
		{
			name: "loads a skill matching the request",
			store: func() *evalSkillStore {
				return &evalSkillStore{listed: []customprompts.CustomPrompt{incidentUpdateSkill, haikuSkill}}
			},
			message:    "Write an incident status update: the database is back up after a 20 minute outage.",
			wantMarker: true,
			wantRubrics: []string{
				"has a line starting with STATUS:, a line starting with IMPACT:, and a line starting with NEXT:",
			},
		},
		{
			name: "does not load a skill for an unrelated request",
			store: func() *evalSkillStore {
				return &evalSkillStore{listed: []customprompts.CustomPrompt{incidentUpdateSkill}}
			},
			message: "What is the capital of France?",
		},
		{
			name: "refreshes skills the user says they added",
			store: func() *evalSkillStore {
				return &evalSkillStore{listed: []customprompts.CustomPrompt{haikuSkill}, added: []customprompts.CustomPrompt{incidentUpdateSkill}, listCalls: 1}
			},
			message:    "I just created a skill for incident status updates. Use it to write an update: the database is back up after a 20 minute outage.",
			wantMarker: true,
		},
	}

	for _, config := range evalConfigs {
		evals.Run(t, "skills "+config.name, func(t *evals.EvalT) {
			scenario, post := dmSkillScenario(config.store(), nil, config.message)
			response := respondAsAgent(t, scenario, post)
			assert.NotEmpty(t, response)
			if config.wantMarker {
				assert.Contains(t, response, skillEvalMarker, "the agent should have loaded and followed the skill")
			} else {
				assert.NotContains(t, response, skillEvalMarker, "the agent should not have loaded the skill")
			}
			for _, rubric := range config.wantRubrics {
				evals.LLMRubricT(t, rubric, response)
			}
		})
	}
}

func TestBuiltinSummarizeSkillEval(t *testing.T) {
	readChannel := llm.Tool{
		Name:        "read_channel",
		Description: "Read recent posts from a Mattermost channel. Parameters: channel_id (required), limit (1-100, default 20).",
		Schema: llm.NewJSONSchemaFromStruct[struct {
			ChannelID string `json:"channel_id"`
			Limit     int    `json:"limit,omitempty"`
		}](),
		AutoExecute: true,
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			return `Channel: Town Square (ID: ` + skillEvalChannelID + `)
Post ID: launchpost1234567890abcde | @dana: We're moving the launch to Thursday because QA found a blocker in checkout.
Post ID: qafixpost12345678901234ab | @sam: I'll have the checkout fix up for review by Wednesday noon.
Post ID: docspost123456789012345ab | @lee: Docs for the new pricing page are ready, waiting on final copy from marketing.`, nil
		},
	}

	evals.Run(t, "skills built-in summarize skill cites posts", func(t *evals.EvalT) {
		scenario, post := dmSkillScenario(&evalSkillStore{}, []llm.Tool{readChannel}, "Catch me up on what's been happening in the channel with ID "+skillEvalChannelID+".")
		response := respondAsAgent(t, scenario, post)
		assert.Contains(t, response, "view=citation", "the summary should use the skill's citation format")
		evals.LLMRubricT(t, "mentions that the launch moved to Thursday", response)
	})
}
