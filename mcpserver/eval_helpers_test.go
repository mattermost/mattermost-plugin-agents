// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/testhelpers"
	"github.com/mattermost/mattermost/server/public/model"
)

// evalChannelData holds the seeded test data for eval tests.
type evalChannelData struct {
	team    *model.Team
	channel *model.Channel
	// designChannel is a second channel with unrelated content for cross-channel search testing.
	designChannel *model.Channel
	alice         *model.User
	bob           *model.User
	charlie       *model.User
	// bobTimelinePost is Bob's "What's the timeline?" post, used as a thread root.
	bobTimelinePost *model.Post
	// aliceRollbackPost is Alice's description of the migration rollback plan.
	aliceRollbackPost *model.Post
}

// seedChannelConversation creates a realistic multi-user conversation in a channel
// with known content that rubrics can check against.
func seedChannelConversation(t *testing.T, serverURL, adminToken string) *evalChannelData {
	t.Helper()

	ctx := context.Background()
	adminClient := model.NewAPIv4Client(serverURL)
	adminClient.SetToken(adminToken)

	// Create team and channel
	team := testhelpers.CreateTestTeam(t, adminClient, "eval-team", "Eval Team")
	channel := testhelpers.CreateTestChannel(t, adminClient, team.Id, "migration-planning", "Migration Planning")

	// Create users with known passwords so we can log in as them
	password := "EvalTest123!"
	alice := testhelpers.CreateTestUser(t, adminClient, "alice.eval", "alice.eval@example.com", password)
	bob := testhelpers.CreateTestUser(t, adminClient, "bob.eval", "bob.eval@example.com", password)
	charlie := testhelpers.CreateTestUser(t, adminClient, "charlie.eval", "charlie.eval@example.com", password)

	// Add users to team and channel
	for _, u := range []*model.User{alice, bob, charlie} {
		testhelpers.AddUserToTeam(t, adminClient, team.Id, u.Id)
		testhelpers.AddUserToChannel(t, adminClient, channel.Id, u.Id)
	}

	// Create per-user clients
	aliceClient := model.NewAPIv4Client(serverURL)
	_, _, err := aliceClient.Login(ctx, alice.Username, password)
	require.NoError(t, err, "Failed to login as alice")

	bobClient := model.NewAPIv4Client(serverURL)
	_, _, err = bobClient.Login(ctx, bob.Username, password)
	require.NoError(t, err, "Failed to login as bob")

	charlieClient := model.NewAPIv4Client(serverURL)
	_, _, err = charlieClient.Login(ctx, charlie.Username, password)
	require.NoError(t, err, "Failed to login as charlie")

	// Post messages in order to create a realistic conversation.
	// Alice proposes PostgreSQL migration
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "I've been evaluating our database options and I think we should migrate from MySQL to PostgreSQL. The JSON support and extension ecosystem are much better for our use case.",
	})
	require.NoError(t, err)

	// Bob asks about timeline
	bobTimelinePost, _, err := bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "What's the timeline for this? We have the Q3 feature freeze coming up.",
	})
	require.NoError(t, err)

	// Alice replies in-thread to Bob's question
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		RootId:    bobTimelinePost.Id,
		Message:   "I'm targeting next sprint for the schema migration, then two weeks of testing before we cut over.",
	})
	require.NoError(t, err)

	// Charlie raises concern about data volume
	_, _, err = charlieClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "One concern — we have about 500GB of data in the current MySQL instance. Have we estimated the downtime for the migration?",
	})
	require.NoError(t, err)

	// Bob @mentions alice asking about rollback plan
	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "@alice.eval what's the rollback plan if something goes wrong during the migration?",
	})
	require.NoError(t, err)

	// Alice describes rollback approach
	aliceRollbackPost, _, err := aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "We'll keep the MySQL instance running in read-only mode during the cutover. If anything fails, we can switch back within minutes. I've also set up continuous replication as a safety net.",
	})
	require.NoError(t, err)

	// Charlie shares monitoring docs link
	_, _, err = charlieClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "Good plan. I've put together monitoring dashboards for tracking the migration progress: https://wiki.example.com/pg-migration-monitoring",
	})
	require.NoError(t, err)

	// Bob confirms he'll review
	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "Sounds solid. I'll review the migration script this week and flag any issues before we start.",
	})
	require.NoError(t, err)

	// Create a second channel with unrelated content for cross-channel search testing
	designChannel := testhelpers.CreateTestChannel(t, adminClient, team.Id, "design-review", "Design Review")
	for _, u := range []*model.User{alice, bob} {
		testhelpers.AddUserToChannel(t, adminClient, designChannel.Id, u.Id)
	}

	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: designChannel.Id,
		Message:   "I've put together the initial Figma mockups for the new dashboard redesign. The layout focuses on key metrics visibility.",
	})
	require.NoError(t, err)

	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: designChannel.Id,
		Message:   "The design system components look great. I think we should use the card-based layout for the analytics section.",
	})
	require.NoError(t, err)

	return &evalChannelData{
		team:              team,
		channel:           channel,
		designChannel:     designChannel,
		alice:             alice,
		bob:               bob,
		charlie:           charlie,
		bobTimelinePost:   bobTimelinePost,
		aliceRollbackPost: aliceRollbackPost,
	}
}

// evalTeamBroadcastData holds seeded data for the team broadcast DM eval.
type evalTeamBroadcastData struct {
	team    *model.Team
	dana    *model.User
	emma    *model.User
	frank   *model.User
	botUser *model.User // user account backing the bot
}

// seedTeamBroadcastScenario creates a team with real users and a bot account
// for testing the "DM everyone on a team" workflow.
func seedTeamBroadcastScenario(t *testing.T, serverURL, adminToken string) *evalTeamBroadcastData {
	t.Helper()

	ctx := context.Background()
	adminClient := model.NewAPIv4Client(serverURL)
	adminClient.SetToken(adminToken)

	// Enable bot account creation
	_, _, err := adminClient.PatchConfig(ctx, &model.Config{
		ServiceSettings: model.ServiceSettings{
			EnableBotAccountCreation: model.NewPointer(true),
		},
	})
	require.NoError(t, err, "Failed to enable bot account creation")

	// Create team with display name "Staff" and URL name "private-core"
	team := testhelpers.CreateTestTeam(t, adminClient, "private-core", "Staff")

	// Create real users with first/last names so they're clearly human
	password := "EvalTest123!"
	dana := testhelpers.CreateTestUser(t, adminClient, "dana.eval", "dana.eval@example.com", password)
	emma := testhelpers.CreateTestUser(t, adminClient, "emma.eval", "emma.eval@example.com", password)
	frank := testhelpers.CreateTestUser(t, adminClient, "frank.eval", "frank.eval@example.com", password)

	// Set first/last names
	for _, pair := range []struct {
		user      *model.User
		firstName string
		lastName  string
	}{
		{dana, "Dana", "Rodriguez"},
		{emma, "Emma", "Chen"},
		{frank, "Frank", "Williams"},
	} {
		pair.user.FirstName = pair.firstName
		pair.user.LastName = pair.lastName
		_, _, updateErr := adminClient.UpdateUser(ctx, pair.user)
		require.NoError(t, updateErr, "Failed to update user %s", pair.user.Username)
	}

	// Create a bot account
	bot, _, err := adminClient.CreateBot(ctx, &model.Bot{
		Username:    "autobot.eval",
		DisplayName: "Automation Bot",
		Description: "An automated integration bot",
	})
	require.NoError(t, err, "Failed to create bot")

	botUser, _, err := adminClient.GetUser(ctx, bot.UserId, "")
	require.NoError(t, err, "Failed to get bot user")

	// Add all users to the team
	for _, u := range []*model.User{dana, emma, frank, botUser} {
		testhelpers.AddUserToTeam(t, adminClient, team.Id, u.Id)
	}

	return &evalTeamBroadcastData{
		team:    team,
		dana:    dana,
		emma:    emma,
		frank:   frank,
		botUser: botUser,
	}
}

// evalStreamLogger wraps a LanguageModel to log intermediate LLM text and tool call
// requests between tool loop iterations. It wraps the LLM for eval logging so it
// captures each re-invocation of the LLM.
type evalStreamLogger struct {
	inner       llm.LanguageModel
	t           *testing.T
	mu          sync.Mutex
	calledTools []string
	toolCalls   []llm.ToolCall
}

// CalledTools returns a copy of all tool names invoked during the eval run.
// Safe to call after ReadAll() completes.
func (w *evalStreamLogger) CalledTools() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, len(w.calledTools))
	copy(out, w.calledTools)
	return out
}

// ToolCalls returns a copy of every tool call, with arguments, made during
// the eval run. Safe to call after ReadAll() completes.
func (w *evalStreamLogger) ToolCalls() []llm.ToolCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]llm.ToolCall, len(w.toolCalls))
	copy(out, w.toolCalls)
	return out
}

func (w *evalStreamLogger) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	result, err := w.inner.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return nil, err
	}

	output := make(chan llm.TextStreamEvent)
	go func() {
		defer close(output)
		var text strings.Builder
		for event := range result.Stream {
			switch event.Type {
			case llm.EventTypeText:
				if s, ok := event.Value.(string); ok {
					text.WriteString(s)
				}
			case llm.EventTypeToolCalls:
				if text.Len() > 0 {
					w.t.Logf("[LLM text]\n%s", text.String())
					text.Reset()
				}
				if tcs, ok := event.Value.([]llm.ToolCall); ok {
					w.mu.Lock()
					for _, tc := range tcs {
						w.t.Logf("[LLM tool call] %s args=%s", tc.Name, string(tc.Arguments))
						w.calledTools = append(w.calledTools, tc.Name)
						w.toolCalls = append(w.toolCalls, tc)
					}
					w.mu.Unlock()
				}
			case llm.EventTypeEnd:
				if text.Len() > 0 {
					w.t.Logf("[LLM final text]\n%s", text.String())
					text.Reset()
				}
			}
			output <- event
		}
	}()

	return &llm.TextStreamResult{Stream: output}, nil
}

func (w *evalStreamLogger) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	return w.inner.ChatCompletionNoStream(ctx, request, opts...)
}

func (w *evalStreamLogger) CountTokens(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	return w.inner.CountTokens(ctx, request, opts...)
}

func (w *evalStreamLogger) InputTokenLimit() int {
	return w.inner.InputTokenLimit()
}

func (w *evalStreamLogger) OutputTokenLimit() int {
	return w.inner.OutputTokenLimit()
}

// agenticEvalSetup holds the components needed for agentic flow evals.
type agenticEvalSetup struct {
	llm          llm.LanguageModel
	llmContext   *llm.Context
	allToolNames []string
	logger       *evalStreamLogger
}

// setupAgenticEval builds the common infrastructure for agentic flow evals:
// MCP tools as llm.Tool, ToolStore, ToolRunner, and populated llm.Context.
func setupAgenticEval(t *testing.T, e *evals.EvalT, suite *TestSuite, requestingUser *model.User, team *model.Team) *agenticEvalSetup {
	t.Helper()

	mcpTools := mcpToolsToLLMTools(t, suite.mcpServer.GetMCPServer())
	require.NotEmpty(t, mcpTools, "Should have MCP tools")

	allToolNames := make([]string, len(mcpTools))
	for i, tool := range mcpTools {
		allToolNames[i] = tool.Name
	}

	toolStore := llm.NewToolStore()
	toolStore.AddTools(mcpTools)

	llmContext := llm.NewContext()
	llmContext.Tools = toolStore
	llmContext.RequestingUser = requestingUser
	llmContext.Channel = &model.Channel{Type: model.ChannelTypeDirect}
	llmContext.Team = team
	llmContext.ServerName = "Eval Server"
	llmContext.CompanyName = "Eval Corp"
	llmContext.BotName = "AI Assistant"
	llmContext.BotUsername = "ai-bot"
	llmContext.BotModel = "eval-model"

	loggedLLM := &evalStreamLogger{inner: e.LLM, t: t}

	return &agenticEvalSetup{
		llm:          loggedLLM,
		llmContext:   llmContext,
		allToolNames: allToolNames,
		logger:       loggedLLM,
	}
}

// mcpToolsToLLMTools converts MCP server tools into llm.Tool instances with resolvers
// that call through the MCP protocol.
func mcpToolsToLLMTools(t *testing.T, mcpServer *gomcp.Server) []llm.Tool {
	t.Helper()

	session := testhelpers.CreateTestMCPSession(t, mcpServer)

	ctx := context.Background()
	toolsList, err := session.ListTools(ctx, nil)
	require.NoError(t, err, "Failed to list MCP tools")

	var tools []llm.Tool
	for _, tool := range toolsList.Tools {
		toolName := tool.Name // capture for closure
		tools = append(tools, llm.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
			Resolver: func(ctx context.Context, _ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				var args map[string]any
				if err := argsGetter(&args); err != nil {
					return "", err
				}
				result, err := session.CallTool(ctx, &gomcp.CallToolParams{
					Name:      toolName,
					Arguments: args,
				})
				if err != nil {
					return "", err
				}
				// Extract text content
				var text string
				for _, c := range result.Content {
					if tc, ok := c.(*gomcp.TextContent); ok {
						text += tc.Text
					}
				}
				return text, nil
			},
		})
	}
	return tools
}

// client4FileContentService implements tools.FileContentService using a Client4
// admin connection. The eval container has no plugin installed, so the production
// HTTPFileContentService callback would 404; this reads files directly over REST
// instead, exercising the real files.Slice ranging logic. Per-user permission
// enforcement is covered by unit tests, so this trusts the admin token.
type client4FileContentService struct {
	serverURL string
	token     string
}

func (s *client4FileContentService) GetContent(ctx context.Context, _ string, fileID string, offset, limit int) (files.Content, error) {
	client := model.NewAPIv4Client(s.serverURL)
	client.SetToken(s.token)

	info, _, err := client.GetFileInfo(ctx, fileID)
	if err != nil {
		return files.Content{}, err
	}

	// Client4 cannot read server-extracted document text (FileInfo.Content is
	// json:"-"), so only plain-text files are readable here — which is all the
	// eval seeds for the readable cases. Non-text files report no extractable
	// text, exactly as production does.
	if !strings.HasPrefix(info.MimeType, "text/") {
		return files.Content{Name: info.Name, MimeType: info.MimeType, HasText: false}, nil
	}

	data, _, err := client.GetFile(ctx, fileID)
	if err != nil {
		return files.Content{}, err
	}

	return files.Slice(info.Name, info.MimeType, string(data), offset, limit), nil
}

// seedFileScenario creates a team and channel (the admin is a member as the
// creator), uploads a file with the given content, and attaches it to a post.
// Returns the team, channel, and the attached file's info.
func seedFileScenario(t *testing.T, serverURL, adminToken, filename string, content []byte) (*model.Team, *model.Channel, *model.FileInfo) {
	t.Helper()

	ctx := context.Background()
	adminClient := model.NewAPIv4Client(serverURL)
	adminClient.SetToken(adminToken)

	suffix := model.NewId()[:8]
	team := testhelpers.CreateTestTeam(t, adminClient, "file-eval-"+suffix, "File Eval Team")
	channel := testhelpers.CreateTestChannel(t, adminClient, team.Id, "file-eval-"+suffix, "File Eval")

	upload, _, err := adminClient.UploadFile(ctx, content, channel.Id, filename)
	require.NoError(t, err, "Failed to upload file")
	require.NotEmpty(t, upload.FileInfos, "Upload should return file info")
	fileInfo := upload.FileInfos[0]

	_, _, err = adminClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "Here's the file.",
		FileIds:   []string{fileInfo.Id},
	})
	require.NoError(t, err, "Failed to create post with file attachment")

	return team, channel, fileInfo
}
