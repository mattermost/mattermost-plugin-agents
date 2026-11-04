// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	ambientEvalBotUsername = "matty"
	ambientEvalTeamID      = "ambevalteamid1234567890123"
	ambientEvalChannelID   = "ambevalchannelid1234567890"
)

// ambientEvalUsers are the channel members, keyed by username. Every case
// draws its authors from here.
var ambientEvalUsers = map[string]*model.User{
	ambientEvalBotUsername: {Id: "mattybotid1234567890123abc", Username: ambientEvalBotUsername, FirstName: "Matty", IsBot: true, Locale: "en"},
	"alice":                {Id: "aliceuserid1234567890123ab", Username: "alice", FirstName: "Alice", LastName: "Chen", Locale: "en"},
	"sarah":                {Id: "sarahuserid1234567890123ab", Username: "sarah", FirstName: "Sarah", LastName: "Kim", Locale: "en"},
	"devon":                {Id: "devonuserid1234567890123ab", Username: "devon", FirstName: "Devon", LastName: "Patel", Locale: "en"},
	"priya":                {Id: "priyauserid1234567890123ab", Username: "priya", FirstName: "Priya", LastName: "Raman", Locale: "en"},
}

// ambientEvalStart is when every case's thread begins.
var ambientEvalStart = time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC)

const (
	ambientEvalDeployInstructions   = "You are the deployment pipeline assistant for the Release Engineering channel. Only reply to questions about the deployment pipeline: builds, deploys, and rollbacks."
	ambientEvalIncidentInstructions = "Reply whenever someone reports a production incident, with the incident runbook link and the first triage steps."
	ambientEvalRunbookInstructions  = "You help the platform team with runbooks and step-by-step operational how-tos, such as credentials, API key rotation, and access requests."
)

// ambientEvalMessage is one post of a case's thread: the first is the root
// and the last is the post being classified.
type ambientEvalMessage struct {
	author  string
	minute  int
	message string
}

// ambientEvalLLM keeps the classifier's raw answer for the eval record.
type ambientEvalLLM struct {
	llm.LanguageModel
	raw string
}

func (m *ambientEvalLLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	raw, err := m.LanguageModel.ChatCompletionNoStream(ctx, request, opts...)
	m.raw = raw
	return raw, err
}

func TestAmbientClassifierEval(t *testing.T) {
	agentAnswer := func(minute int, message string) ambientEvalMessage {
		return ambientEvalMessage{author: ambientEvalBotUsername, minute: minute, message: message}
	}
	stagingApprovalThread := []ambientEvalMessage{
		{"devon", 0, "deploy to staging is stuck at 'waiting for approval'"},
		agentAnswer(1, "Staging deploys need sign-off from someone with the `deploy-approver` role. Open the pipeline run, go to **Pending approvals**, and approve the `staging` stage. If nobody with the role is around, ask in ~release-approvers."),
	}

	tests := []struct {
		name         string
		mode         autoreply.Mode
		instructions string
		thread       []ambientEvalMessage
		shouldReply  bool
	}{
		// ambient, no instructions
		{
			name: "named request without an at-mention",
			mode: autoreply.ModeAmbient,
			thread: []ambientEvalMessage{
				{"devon", 0, "Recap thread for the Q3 release freeze discussion from this morning's sync."},
				{"sarah", 3, "My notes: code freeze starts Oct 28, only P0 fixes after that, and Priya owns the go/no-go call."},
				{"priya", 5, "+1, and we agreed the mobile release slips a week so it doesn't overlap with the freeze."},
				{"alice", 9, "Matty, can you summarize what we decided here so I can paste it into the release doc?"},
			},
			shouldReply: true,
		},
		{
			name: "named imperative in a top-level post",
			mode: autoreply.ModeAmbient,
			thread: []ambientEvalMessage{
				{"alice", 0, "matty draft a short announcement for the maintenance window this Saturday 02:00-04:00 UTC, chat will be read-only during it"},
			},
			shouldReply: true,
		},
		{
			name: "social chat between teammates",
			mode: autoreply.ModeAmbient,
			thread: []ambientEvalMessage{
				{"sarah", 0, "Did anyone catch the match last night?"},
				{"devon", 2, "Yes! That stoppage-time goal was unreal"},
				{"sarah", 3, "I still can't believe the ref waved off the first one for offside 😤"},
			},
			shouldReply: false,
		},
		{
			name: "talking about the agent in the third person",
			mode: autoreply.ModeAmbient,
			thread: []ambientEvalMessage{
				{"devon", 0, "The checkout integration test has been flaky all week"},
				{"alice", 2, "I asked Matty about it yesterday and it confidently told me to just delete the test lol"},
			},
			shouldReply: false,
		},
		{
			name: "question addressed to a named teammate",
			mode: autoreply.ModeAmbient,
			thread: []ambientEvalMessage{
				{"alice", 0, "Checkout tests are red on main since this morning's merge"},
				{"devon", 4, "Looks like the payment mock changed its signature"},
				{"alice", 6, "Sarah, did you already push the fix for the payment mock?"},
			},
			shouldReply: false,
		},
		{
			name: "rhetorical venting",
			mode: autoreply.ModeAmbient,
			thread: []ambientEvalMessage{
				{"devon", 0, "why is CI always broken on Fridays. every. single. week. 🙃"},
			},
			shouldReply: false,
		},
		{
			name: "terse follow-up to the agent's answer in a channel thread",
			mode: autoreply.ModeAmbient,
			thread: []ambientEvalMessage{
				{"priya", 0, "How do I clear the local build cache for the webapp?"},
				agentAnswer(1, "Run `make clean` from the repo root, then `rm -rf webapp/node_modules/.cache`. The next `make dist` rebuilds everything from scratch."),
				{"priya", 4, "and on Windows?"},
			},
			shouldReply: true,
		},
		// ambient, with instructions
		{
			name:         "implicit how-to request without a question mark",
			mode:         autoreply.ModeAmbient,
			instructions: ambientEvalRunbookInstructions,
			thread: []ambientEvalMessage{
				{"alice", 0, "need the steps to rotate the API key for the staging webhook integration"},
			},
			shouldReply: true,
		},
		{
			name:         "production incident reported without a question",
			mode:         autoreply.ModeAmbient,
			instructions: ambientEvalIncidentInstructions,
			thread: []ambientEvalMessage{
				{"devon", 0, "prod checkout is throwing 500s again"},
			},
			shouldReply: true,
		},
		{
			name:         "incident thread being closed out",
			mode:         autoreply.ModeAmbient,
			instructions: ambientEvalIncidentInstructions,
			thread: []ambientEvalMessage{
				{"devon", 0, "prod checkout is throwing 500s again"},
				agentAnswer(1, "Incident runbook: https://wiki.example.com/runbooks/checkout. First steps: check the payment gateway dashboard for upstream errors, confirm the latest deploy time, and page the payments on-call if the error rate stays above 2% for 5 minutes."),
				{"sarah", 6, "on it. looks like the payment gateway TLS cert expired at 08:55"},
				{"devon", 21, "cert renewed, error rate is back to baseline. thanks all, closing this out"},
			},
			shouldReply: false,
		},
		{
			name:         "room question within the agent's instructions",
			mode:         autoreply.ModeAmbient,
			instructions: ambientEvalDeployInstructions,
			thread: []ambientEvalMessage{
				{"sarah", 0, "does anyone know how to roll back a release? 4.2.0 broke the login page on staging"},
			},
			shouldReply: true,
		},
		{
			name:         "room question outside the agent's instructions",
			mode:         autoreply.ModeAmbient,
			instructions: ambientEvalDeployInstructions,
			thread: []ambientEvalMessage{
				{"priya", 0, "anyone want to grab lunch at the taco place around 12:30?"},
			},
			shouldReply: false,
		},
		{
			name:         "on-topic question addressed to a teammate",
			mode:         autoreply.ModeAmbient,
			instructions: ambientEvalDeployInstructions,
			thread: []ambientEvalMessage{
				{"alice", 0, "@devon are you running the 4.2.1 production deploy tonight or should I?"},
			},
			shouldReply: false,
		},
		// ambient_threads, no instructions
		{
			name: "follow-up question to the agent",
			mode: autoreply.ModeAmbientThreads,
			thread: []ambientEvalMessage{
				{"alice", 0, "How do I add a teammate to the on-call rotation?"},
				agentAnswer(1, "In PagerDuty, open **Schedules → Platform Primary → Edit** and add them under **Layers**. The change takes effect at the next handoff."),
				{"alice", 5, "Can you also show me how to do that with the pd CLI instead of the UI?"},
			},
			shouldReply: true,
		},
		{
			name: "terse follow-up to the agent",
			mode: autoreply.ModeAmbientThreads,
			thread: []ambientEvalMessage{
				{"alice", 0, "How do I get read-only credentials for the prod reporting database?"},
				agentAnswer(1, "Request them through Vault: `vault read database/creds/reporting-prod-ro`. The lease lasts 8 hours and you need to be in the `reporting-readers` group."),
				{"alice", 3, "and for staging?"},
			},
			shouldReply: true,
		},
		{
			name: "correction of the agent's answer",
			mode: autoreply.ModeAmbientThreads,
			thread: []ambientEvalMessage{
				{"priya", 0, "Which flag turns on the new search indexer?"},
				agentAnswer(1, "Start the server with `--enable-search-indexer`. It's off by default."),
				{"priya", 4, "that's not right, the flag was renamed in v2"},
			},
			shouldReply: true,
		},
		{
			name: "thanks after the agent answered",
			mode: autoreply.ModeAmbientThreads,
			thread: []ambientEvalMessage{
				{"sarah", 0, "What's the command to tail the API gateway logs in staging?"},
				agentAnswer(1, "`kubectl logs -n staging deploy/api-gateway -f --since=10m`"),
				{"sarah", 2, "thanks!"},
			},
			shouldReply: false,
		},
		{
			name: "side conversation between teammates",
			mode: autoreply.ModeAmbientThreads,
			thread: []ambientEvalMessage{
				{"devon", 0, "What's the fastest way to reproduce the websocket reconnect bug locally?"},
				agentAnswer(1, "Open the app, then in DevTools → Network switch to **Offline** for about 30 seconds and back to **Online**. The reconnect loop shows up in the console right after."),
				{"alice", 7, "devon want to pair on this after standup?"},
				{"devon", 8, "sure, 10:30 works. I'll grab the small meeting room"},
			},
			shouldReply: false,
		},
		// ambient_threads, with instructions
		{
			name:         "off-topic aside in the agent's thread",
			mode:         autoreply.ModeAmbientThreads,
			instructions: ambientEvalDeployInstructions,
			thread: slices.Concat(stagingApprovalThread, []ambientEvalMessage{
				{"priya", 6, "unrelated, but is anyone else going to the offsite next week?"},
			}),
			shouldReply: false,
		},
		{
			name:         "new failure reported back to the agent",
			mode:         autoreply.ModeAmbientThreads,
			instructions: ambientEvalDeployInstructions,
			thread: slices.Concat(stagingApprovalThread, []ambientEvalMessage{
				{"devon", 9, "ok approved it, now it's failing at the smoke-test step with exit code 137"},
			}),
			shouldReply: true,
		},
	}

	for _, tc := range tests {
		evals.Run(t, "ambient classifier "+tc.name, func(e *evals.EvalT) {
			botUser := ambientEvalUsers[ambientEvalBotUsername]
			channel := &model.Channel{Id: ambientEvalChannelID, TeamId: ambientEvalTeamID, Type: model.ChannelTypeOpen, Name: "release-eng", DisplayName: "Release Engineering"}

			mmClient := mmapimocks.NewMockClient(e.T)
			for _, user := range ambientEvalUsers {
				mmClient.EXPECT().GetUser(user.Id).Return(user, nil).Maybe()
			}
			mmClient.EXPECT().LogWarn(mock.Anything, mock.Anything).Maybe()

			posts := make([]*model.Post, len(tc.thread))
			list := model.NewPostList()
			for i, msg := range tc.thread {
				author, ok := ambientEvalUsers[msg.author]
				require.True(e, ok, "unknown author %q", msg.author)
				post := &model.Post{
					Id:        model.NewId(),
					ChannelId: channel.Id,
					UserId:    author.Id,
					Message:   msg.message,
					CreateAt:  ambientEvalStart.Add(time.Duration(msg.minute) * time.Minute).UnixMilli(),
				}
				if i > 0 {
					post.RootId = posts[0].Id
				}
				posts[i] = post
				list.AddPost(post)
				list.AddOrder(post.Id)
			}
			trigger := posts[len(posts)-1]
			mmClient.EXPECT().GetPostThread(posts[0].Id).Return(list, nil).Maybe()

			mockAPI := &plugintest.API{}
			mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
			mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced}).Maybe()
			mockAPI.On("GetTeam", ambientEvalTeamID).Return(&model.Team{Id: ambientEvalTeamID, Name: "platform", DisplayName: "Platform"}, nil).Maybe()

			promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
			require.NoError(e, err)
			c := &Conversations{
				prompts:        promptsManager,
				mmClient:       mmClient,
				contextBuilder: llmcontext.NewLLMContextBuilder(pluginapi.NewClient(mockAPI, nil), nil, nil, nil),
			}

			classifierLLM := &ambientEvalLLM{LanguageModel: llmtest.NewLanguageModelTestLogWrapper(e.T, e.LLM)}
			bot := bots.NewBot(
				llm.BotConfig{
					ID:                 botUser.Id,
					Name:               ambientEvalBotUsername,
					DisplayName:        "Matty",
					ChannelAccessLevel: llm.ChannelAccessLevelAll,
					UserAccessLevel:    llm.UserAccessLevelAll,
				},
				llm.ServiceConfig{ID: e.Provider, DefaultModel: e.Model},
				&model.Bot{UserId: botUser.Id, Username: ambientEvalBotUsername, DisplayName: "Matty"},
				classifierLLM,
			)
			setting := &autoreply.Setting{ChannelID: channel.Id, BotID: botUser.Id, Mode: tc.mode, Instructions: tc.instructions}

			// handleAutoReply would go on to generate the full reply when the
			// classifier says yes; the eval grades only the decision, so it
			// runs the classifier steps of that gate directly.
			thread, err := c.ambientThreadPosts(trigger)
			require.NoError(e, err)
			if tc.mode == autoreply.ModeAmbientThreads {
				require.NotEmpty(e, trigger.RootId, "ambient_threads only classifies thread replies")
				require.True(e, threadHasReplyFrom(thread, botUser.Id), "ambient_threads only classifies threads the agent replied in")
			}

			postingUser := ambientEvalUsers[tc.thread[len(tc.thread)-1].author]
			classifyErr := c.classifyAmbientReply(context.Background(), bot, setting, trigger, postingUser, channel, thread)
			shouldReply := classifyErr == nil
			if !shouldReply && !errors.Is(classifyErr, errAmbientDeclined) {
				require.NoError(e, classifyErr, "the classifier failed instead of deciding; raw output %q", classifierLLM.raw)
			}

			result := &evals.EvalResult{
				Rubric:    fmt.Sprintf("decides should_reply=%t", tc.shouldReply),
				Output:    classifierLLM.raw,
				Reasoning: fmt.Sprintf("mode %s, instructions set: %t; classified post by @%s: %q; decided should_reply=%t", tc.mode, tc.instructions != "", postingUser.Username, trigger.Message, shouldReply),
				Pass:      shouldReply == tc.shouldReply,
			}
			if result.Pass {
				result.Score = 1
			}
			evals.RecordScore(e, result)
			// The verdict is already recorded, so it bypasses EvalT's capture of
			// assertion failures.
			assert.Equal(e.T, tc.shouldReply, shouldReply, "classifier decision for %q", trigger.Message)
		})
	}
}
