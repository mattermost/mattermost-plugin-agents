// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channels

import (
	stdcontext "context"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// RequiredMCPTools are the embedded MCP tools channel analysis binds to the
// analyzed channel.
var RequiredMCPTools = []llm.EnabledMCPTool{
	{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_channel"},
	{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "get_channel_info"},
}

// ToolBot returns the bot to load channel-analysis tools for. Channel analysis
// immediately scopes the catalog to bound read-only tools, so the MCP catalog
// is loaded even when the agent uses a narrower allowlist.
func ToolBot(bot *bots.Bot) *bots.Bot {
	cfg := bot.GetConfig()
	if cfg.AutoEnableNewMCPTools {
		return bot
	}
	cfg.AutoEnableNewMCPTools = true
	cfg.EnabledMCPTools = nil
	return bot.WithConfig(cfg)
}

// AnalysisResult is the return type for channel analysis operations.
// It contains the conversation ID (for linking to the post) and the
// final LLM stream to be consumed by the streaming layer.
type AnalysisResult struct {
	ConversationID string
	Stream         *llm.TextStreamResult
}

type Channels struct {
	llm      llm.LanguageModel
	prompts  *llm.Prompts
	client   mmapi.Client
	dbClient *mmapi.DBClient
	convSvc  *conversation.Service
}

func New(
	llm llm.LanguageModel,
	prompts *llm.Prompts,
	client mmapi.Client,
	dbClient *mmapi.DBClient,
	convSvc *conversation.Service,
) *Channels {
	return &Channels{
		llm:      llm,
		prompts:  prompts,
		client:   client,
		dbClient: dbClient,
		convSvc:  convSvc,
	}
}

// AnalyzeChannel uses MCP tools to analyze channel activity based on user request.
// It creates a conversation entity, runs the ToolRunner loop for tool execution,
// persists tool turns, and returns the final stream for the streaming layer.
func (c *Channels) AnalyzeChannel(
	ctx stdcontext.Context,
	context *llm.Context,
	channelID string,
	userID string,
	botID string,
	analysisData map[string]any,
) (*AnalysisResult, error) {
	// Inject analysis data into context for the prompt
	displayName := context.Channel.DisplayName
	if displayName == "" {
		switch context.Channel.Type {
		case model.ChannelTypeDirect:
			displayName = "Direct Message"
		case model.ChannelTypeGroup:
			displayName = "Group Message"
		default:
			displayName = context.Channel.Id
		}
	}

	context.Parameters = map[string]any{
		"Channel": map[string]string{
			"Id":          channelID,
			"DisplayName": displayName,
		},
		"Analysis": analysisData,
	}

	userPrompt := "Please summarize the channel activity as requested."
	operationSubType, _ := analysisData["AnalysisType"].(string)
	if operationSubType == "" {
		operationSubType = llm.TokenUsageUnknown
	}

	if err := bindChannelTools(context, channelID); err != nil {
		return nil, err
	}

	systemPrompt, err := c.prompts.Format(prompts.PromptSummarizeChannelSystem, context)
	if err != nil {
		return nil, fmt.Errorf("failed to format system prompt: %w", err)
	}

	return c.AnalyzeChannelWithRequest(ctx, context, userID, botID, systemPrompt, userPrompt, operationSubType)
}

// bindChannelTools replaces context.Tools with the channel-analysis tools bound
// to channelID so the LLM cannot point them at another channel.
func bindChannelTools(context *llm.Context, channelID string) error {
	readChannel, ok := requiredEmbeddedToolByExactOrBareName(context.Tools, "read_channel")
	if !ok {
		return fmt.Errorf("read_channel tool not available - ensure MCP embedded server is enabled and running")
	}
	getChannelInfo, ok := requiredEmbeddedToolByExactOrBareName(context.Tools, "get_channel_info")
	if !ok {
		return fmt.Errorf("get_channel_info tool not available - ensure MCP embedded server is enabled and running")
	}

	scopedTools := llm.NewToolStore()
	scopedTools.AddTools([]llm.Tool{
		readChannel.WithBoundParams(map[string]any{"channel_id": channelID}),
		getChannelInfo.WithBoundParams(map[string]any{"channel_id": channelID}),
	})
	context.Tools = scopedTools
	return nil
}

func requiredEmbeddedToolByExactOrBareName(store *llm.ToolStore, name string) (llm.Tool, bool) {
	lookup, ok := store.LookupTool(name, mcp.EmbeddedClientKey)
	if !ok {
		return llm.Tool{}, false
	}

	tool := lookup.Tool
	// Channel analysis exposes bound embedded tools under their bare names.
	tool.Name = name
	return tool, true
}

// AnalyzeChannelWithRequest creates a conversation and runs the ToolRunner with
// pre-formatted prompts. This is the core of AnalyzeChannel, split out for
// testability without needing real prompt formatting infrastructure.
// The context must have Tools set to a ToolStore containing the tools to use.
func (c *Channels) AnalyzeChannelWithRequest(
	ctx stdcontext.Context,
	context *llm.Context,
	userID string,
	botID string,
	systemPrompt string,
	userPrompt string,
	operationSubType string,
) (*AnalysisResult, error) {
	// Create conversation entity.
	// Channel analysis is delivered via DM to the requester, so the
	// conversation is owner-only: ChannelID is deliberately not set, making
	// GET /conversations/{id} fall into the threadless (owner-only) branch.
	convResult, err := c.convSvc.CreateConversation(conversation.CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		Operation:    llm.OperationChannelSummary,
		SystemPrompt: systemPrompt,
		UserMessage:  userPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	// Build CompletionRequest from the conversation turns.
	conv, err := c.convSvc.GetConversation(convResult.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	completionRequest, err := c.convSvc.BuildCompletionRequest(conv, context)
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request: %w", err)
	}
	completionRequest.OperationSubType = operationSubType

	stream, err := c.runAnalysis(ctx, *completionRequest, convResult.ConversationID)
	if err != nil {
		return nil, err
	}

	return &AnalysisResult{
		ConversationID: convResult.ConversationID,
		Stream:         stream,
	}, nil
}

// RegenerateAnalysis re-runs a channel analysis inside its existing
// conversation: the stored system prompt and user turn are reused, the prior
// response turns anchored to postID are scrubbed, and the tools are re-bound
// to channelID. The context must carry the unscoped tool store.
func (c *Channels) RegenerateAnalysis(
	ctx stdcontext.Context,
	context *llm.Context,
	conv *store.Conversation,
	channelID string,
	postID string,
	sessionID string,
) (*llm.TextStreamResult, error) {
	if err := bindChannelTools(context, channelID); err != nil {
		return nil, err
	}

	completionRequest, err := c.convSvc.BuildRegenerateRequest(conv, context, postID, conversation.BuildOptions{SessionID: sessionID})
	if err != nil {
		return nil, err
	}

	return c.runAnalysis(ctx, *completionRequest, conv.ID)
}

// runAnalysis runs the ToolRunner loop, always approving the bound tools and
// persisting tool turns to the conversation.
func (c *Channels) runAnalysis(ctx stdcontext.Context, request llm.CompletionRequest, conversationID string) (*llm.TextStreamResult, error) {
	runner := toolrunner.New(c.llm)
	runResult, err := runner.Run(
		ctx,
		request,
		func(_ llm.ToolCall) bool { return true },
		func(turns []toolrunner.ToolTurn) {
			if writeErr := c.convSvc.WriteToolTurns(conversationID, turns, true); writeErr != nil {
				c.client.LogError("Failed to write tool turns", "error", writeErr, "conversation_id", conversationID)
			}
		},
		llm.WithReasoningDisabled(),
	)
	if err != nil {
		return nil, fmt.Errorf("tool runner failed: %w", err)
	}
	return runResult.Stream, nil
}

// Interval fetches posts for a time range and creates a conversation entity
// for the analysis. No tools are used.
func (c *Channels) Interval(
	ctx stdcontext.Context,
	context *llm.Context,
	channelID string,
	userID string,
	botID string,
	startTime int64,
	endTime int64,
	promptName string,
) (*AnalysisResult, error) {
	var posts *model.PostList
	var err error
	if endTime == 0 {
		posts, err = c.client.GetPostsSince(channelID, startTime)
	} else {
		posts, err = c.getPostsByChannelBetween(channelID, startTime, endTime)
	}
	if err != nil {
		return nil, err
	}

	threadData, err := mmapi.GetMetadataForPosts(c.client, posts)
	if err != nil {
		return nil, err
	}

	// Remove deleted posts and system posts (like join/leave messages)
	threadData.Posts = slices.DeleteFunc(threadData.Posts, func(post *model.Post) bool {
		return post.DeleteAt != 0 || post.Type != ""
	})

	formattedThread := format.ThreadData(threadData)

	context.Parameters = map[string]any{
		"Thread": formattedThread,
	}
	systemPrompt, err := c.prompts.Format(promptName, context)
	if err != nil {
		return nil, err
	}

	userPrompt, err := c.prompts.Format(prompts.PromptThreadUser, context)
	if err != nil {
		return nil, err
	}

	return c.IntervalWithRequest(ctx, context, userID, botID, systemPrompt, userPrompt, promptName)
}

// IntervalWithRequest creates a conversation and runs the LLM with pre-formatted
// prompts. This is the core of Interval, split out for testability without
// needing real post-fetching infrastructure.
func (c *Channels) IntervalWithRequest(
	ctx stdcontext.Context,
	context *llm.Context,
	userID string,
	botID string,
	systemPrompt string,
	userPrompt string,
	promptName string,
) (*AnalysisResult, error) {
	// Create conversation entity. Owner-only (see AnalyzeChannelWithRequest).
	convResult, err := c.convSvc.CreateConversation(conversation.CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		Operation:    llm.OperationChannelInterval,
		SystemPrompt: systemPrompt,
		UserMessage:  userPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	// Build CompletionRequest from conversation turns.
	conv, err := c.convSvc.GetConversation(convResult.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	completionRequest, err := c.convSvc.BuildCompletionRequest(conv, context)
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request: %w", err)
	}
	completionRequest.OperationSubType = promptName

	resultStream, err := c.llm.ChatCompletion(ctx, *completionRequest, llm.WithToolsDisabled())
	if err != nil {
		return nil, err
	}

	return &AnalysisResult{
		ConversationID: convResult.ConversationID,
		Stream:         resultStream,
	}, nil
}

const (
	postsPerPage = 60
	maxPosts     = 200
)

func (c *Channels) getPostsByChannelBetween(channelID string, startTime, endTime int64) (*model.PostList, error) {
	// Find the ID of first post in our time range
	firstPostID, err := c.dbClient.GetFirstPostBeforeTimeRangeID(channelID, startTime, endTime)
	if err != nil {
		return nil, err
	}

	// Initialize result list
	result := &model.PostList{
		Posts: make(map[string]*model.Post),
		Order: []string{},
	}

	// Keep fetching previous pages until we either:
	// 1. Reach the endTime
	// 2. Hit the maxPosts limit
	// 3. Run out of posts
	totalPosts := 0
	page := 0

	for totalPosts < maxPosts {
		morePosts, err := c.client.GetPostsBefore(channelID, firstPostID, page, postsPerPage)
		if err != nil {
			return nil, err
		}

		if len(morePosts.Posts) == 0 {
			break // No more posts
		}

		// Add posts that fall within our time range
		for _, post := range morePosts.Posts {
			if post.CreateAt >= startTime && post.CreateAt <= endTime {
				result.Posts[post.Id] = post
				result.Order = append([]string{post.Id}, result.Order...)
				totalPosts++
				if totalPosts >= maxPosts {
					break
				}
			}
			if post.CreateAt < startTime {
				break // We've gone too far back
			}
		}

		page++
	}

	return result, nil
}
