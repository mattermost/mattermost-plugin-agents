// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"maps"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	evalSessionID     = "eval-session"
	evalReplyTimeout  = 3 * time.Minute
	evalServiceModel  = "mattermodel-5.4"
	evalServiceConfig = "eval-service"
)

// evalScenario describes the Mattermost state an agent eval runs against.
type evalScenario struct {
	bot       llm.BotConfig
	botUser   *model.User
	team      *model.Team
	channel   *model.Channel
	users     map[string]*model.User
	thread    *model.PostList
	fileInfos map[string]*model.FileInfo
	files     map[string][]byte
	tools     []llm.Tool
}

type evalReply struct {
	text string
	err  error
}

// evalStreamingService captures the reply that production would stream into
// the agent's response post.
type evalStreamingService struct {
	fakeStreamingService
	started atomic.Bool
	replies chan evalReply
}

func (s *evalStreamingService) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	s.started.Store(true)
	return ctx, nil
}

func (s *evalStreamingService) StreamToPost(_ context.Context, stream *llm.TextStreamResult, _ *model.Post, _ string, _ string) {
	text, err := stream.ReadAll()
	s.replies <- evalReply{text: text, err: err}
}

// evalAgentLLM answers title generation locally: the handler runs it in a
// goroutine that can outlive the eval, and it is not what the eval grades.
type evalAgentLLM struct {
	llm.LanguageModel
}

func (m evalAgentLLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	if request.Operation == llm.OperationTitleGeneration {
		return "Eval conversation", nil
	}
	return m.LanguageModel.ChatCompletionNoStream(ctx, request, opts...)
}

// respondAsAgent posts post through the production MessageHasBeenPosted
// handler, backed by the model under test, and returns the agent's reply.
func respondAsAgent(t *evals.EvalT, scenario evalScenario, post *model.Post) string {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced}).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(scenario.team, nil).Maybe()
	mockAPI.On("GetBotUsers").Return(nil, nil).Maybe()
	// A mention only counts when the bot's account still holds the username.
	mockAPI.On("GetUser", scenario.botUser.Id).Return(scenario.botUser, nil).Maybe()
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe()
		mockAPI.On("LogInfo", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}
	pluginClient := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginClient)

	botService := bots.New(mockAPI, pluginClient, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	agentLLM := evalAgentLLM{LanguageModel: llmtest.NewLanguageModelTestLogWrapper(t.T, t.LLM)}
	botService.SetBotsForTesting([]*bots.Bot{bots.NewBot(
		scenario.bot,
		llm.ServiceConfig{ID: evalServiceConfig, Type: llm.ServiceTypeOpenAI, DefaultModel: evalServiceModel},
		&model.Bot{UserId: scenario.botUser.Id, Username: scenario.botUser.Username, DisplayName: scenario.bot.DisplayName},
		agentLLM,
	)})

	users := map[string]*model.User{scenario.botUser.Id: scenario.botUser}
	maps.Copy(users, scenario.users)
	postThreads := map[string]*model.PostList{}
	if scenario.thread != nil {
		for id := range scenario.thread.Posts {
			postThreads[id] = scenario.thread
		}
	}
	mmClient := &fakeMMClient{
		users:           users,
		channels:        map[string]*model.Channel{scenario.channel.Id: scenario.channel},
		postThreads:     postThreads,
		fileInfos:       scenario.fileInfos,
		files:           scenario.files,
		kv:              map[string]any{},
		allowCreatePost: true,
	}

	promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	contextBuilder := llmcontext.NewLLMContextBuilder(pluginClient, &testToolProvider{tools: scenario.tools}, &testMCPClientManager{}, &mockConfigProvider{})
	streamService := &evalStreamingService{replies: make(chan evalReply, 1)}

	convs := conversations.New(
		promptsManager,
		mmClient,
		streamService,
		contextBuilder,
		botService,
		nil, // db
		licenseChecker,
		i18n.Init(),
		&testToolCallingConfig{},
		newDMPolicyChecker(),
	)
	convs.SetConversationService(conversation.NewService(newFakeConvStore(), promptsManager, mmClient, botService))

	convs.MessageHasBeenPosted(&plugin.Context{SessionId: evalSessionID}, post)
	require.True(t, streamService.started.Load(), "agent did not start a reply; logged errors: %v", mmClient.loggedErrors())

	select {
	case reply := <-streamService.replies:
		require.NoError(t, reply.err, "failed to read the agent's reply stream")
		return reply.text
	case <-time.After(evalReplyTimeout):
		require.FailNow(t, "agent did not reply", "logged errors: %v", mmClient.loggedErrors())
		return ""
	}
}
