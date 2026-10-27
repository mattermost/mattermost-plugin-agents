// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
)

// PostgresTestConnStr is set by TestMain (channel_mention_test.go) when the
// postgres testcontainer is available.
var PostgresTestConnStr string

func newPostgresTestStore(t *testing.T) *store.Store {
	t.Helper()
	if PostgresTestConnStr == "" {
		t.Skip("postgres testcontainer not available")
	}

	admin, err := sqlx.Connect("postgres", PostgresTestConnStr)
	require.NoError(t, err)
	schemaName := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE SCHEMA " + schemaName)
	require.NoError(t, err)

	// The search path goes in the DSN so every pooled connection uses the
	// schema; streaming persists turns from its own goroutine.
	db, err := sqlx.Connect("postgres", PostgresTestConnStr+"&search_path="+schemaName)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec("DROP SCHEMA " + schemaName + " CASCADE")
		admin.Close()
	})

	s := store.New(db)
	require.NoError(t, s.RunMigrations())
	return s
}

// fakeAnthropic is an Anthropic Messages endpoint that answers 400
// invalid_request_error for requests the real API rejects and streams a short
// text reply otherwise. It records every messages request body.
type fakeAnthropic struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (f *fakeAnthropic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if strings.HasSuffix(r.URL.Path, "/count_tokens") {
		_, _ = w.Write([]byte(`{"input_tokens":10}`))
		return
	}
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()

	if violations := llmtest.AnthropicRequestViolations(body); len(violations) > 0 {
		errBody, _ := json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]string{"type": "invalid_request_error", "message": strings.Join(violations, "; ")},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(errBody)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []string{
		`{"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Here is what I found."}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	} {
		var envelope struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &envelope)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.Type, event)
	}
}

func (f *fakeAnthropic) requests() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.bodies...)
}

// wireToolResults maps each tool_result's tool_use_id to its text content in
// an Anthropic request body.
func wireToolResults(t *testing.T, body []byte) map[string]string {
	t.Helper()

	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &req))

	results := map[string]string{}
	for _, msg := range req.Messages {
		var blocks []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type != "tool_result" {
				continue
			}
			var text string
			if json.Unmarshal(block.Content, &text) != nil {
				var parts []struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(block.Content, &parts)
				for _, part := range parts {
					text += part.Text
				}
			}
			results[block.ToolUseID] = text
		}
	}
	return results
}

// TestChannelApprovalsOutOfOrderKeepThreadReplayable reproduces a thread that
// was poisoned on Anthropic: two bot posts in one channel thread each stall on
// two tool calls awaiting approval, and the requester approves and shares the
// newer post first and the older post last. Every follow-up, and a new message
// afterwards, must reach Anthropic as a valid request in which each tool_use is
// answered by its own result.
func TestChannelApprovalsOutOfOrderKeepThreadReplayable(t *testing.T) {
	const (
		botID     = "bot-id"
		userID    = "user12345678901234567890ab"
		channelID = "channel-id"
		rootID    = "post-u1"
	)
	ctx := context.Background()
	s := newPostgresTestStore(t)

	anthropic := &fakeAnthropic{}
	server := httptest.NewServer(anthropic)
	defer server.Close()

	// Native tools route Anthropic requests through the Responses path.
	lm, err := bifrost.New(bifrost.Config{
		Provider:           schemas.Anthropic,
		APIKey:             "test-key",
		APIURL:             server.URL,
		DefaultModel:       "claude-sonnet-4-6",
		StreamingTimeout:   10 * time.Second,
		EnabledNativeTools: []string{llm.NativeToolWebSearch, llm.NativeToolWebFetch},
	})
	require.NoError(t, err)
	defer lm.Shutdown()

	tools := []llm.Tool{
		channelFollowUpTestMCPTool("jira__get_issue", "https://jira.example.com", "fetch a Jira issue"),
		channelFollowUpTestMCPTool("jira__search", "https://jira.example.com", "search Jira"),
	}
	tools[1].Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
		return "", nil
	}

	threadPosts := &model.PostList{Posts: map[string]*model.Post{}}
	var threadMu sync.Mutex
	addThreadPost := func(id, author, message string) *model.Post {
		threadMu.Lock()
		defer threadMu.Unlock()
		post := &model.Post{Id: id, UserId: author, ChannelId: channelID, RootId: rootID, Message: message, CreateAt: int64(1000 * (len(threadPosts.Order) + 1))}
		if id == rootID {
			post.RootId = ""
		}
		threadPosts.AddPost(post)
		threadPosts.AddOrder(id)
		return post
	}

	mmClient := mocks.NewMockClient(t)
	for _, method := range []string{"LogDebug", "LogWarn", "LogError"} {
		mmClient.On(method, mock.Anything).Maybe().Return()
		mmClient.On(method, mock.Anything, mock.Anything).Maybe().Return()
	}
	mmClient.On("GetUser", userID).Maybe().Return(&model.User{Id: userID, Username: "alice"}, nil)
	mmClient.On("GetUser", botID).Maybe().Return(&model.User{Id: botID, Username: "matty", IsBot: true}, nil)
	mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("KVSet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("GetConfig").Maybe().Return(&model.Config{})
	mmClient.On("GetChannel", channelID).Maybe().Return(&model.Channel{Id: channelID, TeamId: "team-id", Type: model.ChannelTypeOpen}, nil)
	mmClient.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("UpdatePost", mock.Anything).Maybe().Return(nil)
	mmClient.EXPECT().GetPostThread(rootID).RunAndReturn(func(string) (*model.PostList, error) {
		threadMu.Lock()
		defer threadMu.Unlock()
		list := model.NewPostList()
		for _, id := range threadPosts.Order {
			list.AddPost(threadPosts.Posts[id].Clone())
			list.AddOrder(id)
		}
		return list, nil
	}).Maybe()

	mmClient.EXPECT().GetPost(mock.Anything).RunAndReturn(func(id string) (*model.Post, error) {
		threadMu.Lock()
		defer threadMu.Unlock()
		if post, ok := threadPosts.Posts[id]; ok {
			return post.Clone(), nil
		}
		return nil, model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusNotFound)
	}).Maybe()

	streamingService := streaming.NewMMPostStreamService(mmClient, i18n.Init())
	streamingService.SetTurnStore(s)

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, true)
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := toolLicenseChecker(t, true)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{bots.NewBot(
		llm.BotConfig{
			ID:                    botID,
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			UserAccessLevel:       llm.UserAccessLevelAll,
			ChannelAccessLevel:    llm.ChannelAccessLevelAll,
		},
		llm.ServiceConfig{DefaultModel: "claude-sonnet-4-6", Type: llm.ServiceTypeAnthropic},
		&model.Bot{UserId: botID, Username: "matty", DisplayName: "Matty"},
		lm,
	)})

	builderAPI := &plugintest.API{}
	mockLicenseState(builderAPI, true)
	builderAPI.On("GetTeam", "team-id").Return(&model.Team{Id: "team-id", Name: "team"}, nil).Maybe()
	for _, method := range []string{"LogDebug", "LogWarn", "LogError"} {
		for args := []any{mock.Anything}; len(args) <= 11; args = append(args, mock.Anything) {
			builderAPI.On(method, args...).Maybe().Return()
		}
	}
	contextBuilder := llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(builderAPI, nil),
		&channelFollowUpTestToolProvider{},
		&channelFollowUpTestMCPToolProvider{tools: tools},
		&channelFollowUpTestConfig{},
	)

	convService := conversation.NewService(s, nil, nil, nil)
	c := &Conversations{
		mmClient:          mmClient,
		contextBuilder:    contextBuilder,
		bots:              botsService,
		convService:       convService,
		streamingService:  streamingService,
		licenseChecker:    licenseChecker,
		configProvider:    &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
		toolPolicyChecker: mapPolicyChecker{},
	}

	userMessage := func(postID, text string) *store.Conversation {
		addThreadPost(postID, userID, text)
		result, createErr := convService.GetOrCreateConversation(conversation.GetOrCreateParams{
			UserID:       userID,
			BotID:        botID,
			ChannelID:    channelID,
			RootPostID:   rootID,
			Operation:    "conversation",
			SystemPrompt: "You are a helpful assistant.",
			UserMessage:  text,
			UserPostID:   new(postID),
		})
		require.NoError(t, createErr)
		return result.Conversation
	}

	// pendingBotPost stores a bot post that stalled on two tool calls awaiting
	// approval, as the streaming finalizer persists it in a channel.
	pendingBotPost := func(conv *store.Conversation, postID string, toolIDs ...string) *model.Post {
		post := addThreadPost(postID, botID, "Let me look that up.")
		post.AddProp(streaming.ConversationIDProp, conv.ID)
		blocks := []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "Let me look that up."}}
		for i, id := range toolIDs {
			blocks = append(blocks, conversation.ContentBlock{
				Type:         conversation.BlockTypeToolUse,
				ID:           id,
				Name:         tools[i].Name,
				ServerOrigin: tools[i].ServerOrigin,
				Input:        json.RawMessage(`{}`),
				Status:       conversation.StatusPending,
				Shared:       new(false),
			})
		}
		content, marshalErr := json.Marshal(blocks)
		require.NoError(t, marshalErr)
		require.NoError(t, s.CreateTurnAutoSequence(&store.Turn{
			ID:             model.NewId(),
			ConversationID: conv.ID,
			PostID:         new(postID),
			Role:           "assistant",
			Content:        content,
		}))
		return post
	}

	channel := &model.Channel{Id: channelID, TeamId: "team-id", Type: model.ChannelTypeOpen}
	approveAndShare := func(post *model.Post, toolIDs ...string) {
		anchor, anchorErr := s.GetTurnByPostID(post.Id)
		require.NoError(t, anchorErr)
		require.NoError(t, c.HandleToolCall(ctx, userID, post, channel, toolIDs, nil))
		require.NoError(t, c.HandleToolResult(ctx, userID, post, channel, toolIDs))
		require.Eventually(t, func() bool {
			current, lookupErr := s.GetTurnByPostID(post.Id)
			return lookupErr == nil && current != nil && current.ID != anchor.ID
		}, 10*time.Second, 20*time.Millisecond, "follow-up stream for %s must finalize", post.Id)
	}

	conv := userMessage(rootID, "@matty what is the status of MM-1?")
	postA := pendingBotPost(conv, "post-a", "a1", "a2")
	userMessage("post-u2", "@matty and the related tickets?")
	postB := pendingBotPost(conv, "post-b", "b1", "b2")

	approveAndShare(postB, "b1", "b2")
	approveAndShare(postA, "a1", "a2")

	conv = userMessage("post-u3", "@matty anything else?")
	threadData, err := mmapi.GetThreadData(mmClient, rootID)
	require.NoError(t, err)
	newMessageReq, err := convService.BuildChannelMentionRequest(conv, &llm.Context{}, threadData)
	require.NoError(t, err)
	stream, err := lm.ChatCompletion(ctx, *newMessageReq)
	require.NoError(t, err)
	_, err = stream.ReadAll()
	assert.NoError(t, err, "a new message in the thread must still reach the LLM")

	requests := anthropic.requests()
	require.Len(t, requests, 3, "expected the two follow-ups and the new message to reach Anthropic")
	assert.Contains(t, string(requests[0]), `"web_fetch`, "requests must take the Responses path, which carries native tools")

	executed := map[string]string{"a1": "mcp:jira__get_issue", "a2": "(no output)", "b1": "mcp:jira__get_issue", "b2": "(no output)"}
	notExecutedA := map[string]string{"a1": conversation.NotExecutedToolResult, "a2": conversation.NotExecutedToolResult, "b1": executed["b1"], "b2": executed["b2"]}
	want := []struct {
		name    string
		results map[string]string
	}{
		{"follow-up for the newer post, older post still pending", notExecutedA},
		{"follow-up for the older post", executed},
		{"new message after both approvals", executed},
	}
	for i, w := range want {
		assert.Empty(t, llmtest.AnthropicRequestViolations(requests[i]), "%s: Anthropic would reject the request:\n%s", w.name, requests[i])
		assert.Equal(t, w.results, wireToolResults(t, requests[i]), "%s: each tool_use must be answered by its own result", w.name)
	}
}
