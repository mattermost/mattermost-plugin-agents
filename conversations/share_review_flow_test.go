// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reviewedToolResult = "PRIVATE-TOOL-RESULT"

var readChannelCall = llm.ToolCall{
	ID:           "tc-read",
	Name:         llm.NamespaceMCPToolName("mattermost", "read_channel"),
	Arguments:    json.RawMessage(`{}`),
	ServerOrigin: mcp.EmbeddedClientKey,
}

// sourceReportingTool is an embedded read tool that records its source
// channels on the request context, as the MCP client does with the source
// channels the embedded server reports in a result's metadata.
func sourceReportingTool(bareName, result string, sources ...*model.Channel) llm.Tool {
	tool := embeddedTestTool(bareName, result)
	tool.Resolver = func(_ context.Context, llmCtx *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
		if llmCtx.Parameters == nil {
			llmCtx.Parameters = map[string]any{}
		}
		existing, _ := llmCtx.Parameters[mcp.SourceChannelsParam].([]*model.Channel)
		llmCtx.Parameters[mcp.SourceChannelsParam] = append(existing, sources...)
		reported, _ := llmCtx.Parameters[mcp.ReportedSourceToolsParam].(map[string]bool)
		if reported == nil {
			reported = map[string]bool{}
		}
		reported[bareName] = true
		llmCtx.Parameters[mcp.ReportedSourceToolsParam] = reported
		return result, nil
	}
	return tool
}

// storedToolResults returns every stored tool_result block with content.
func storedToolResults(env *policyEnv, content string) []conversation.ContentBlock {
	_, turnsByConv := env.convStore.snapshot()
	var out []conversation.ContentBlock
	for _, turns := range turnsByConv {
		for _, turn := range turns {
			blocks, err := conversation.UnmarshalBlocks(turn.Content)
			if err != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type == conversation.BlockTypeToolResult && b.Content == content {
					out = append(out, b)
				}
			}
		}
	}
	return out
}

func requestsMentioning(env *policyEnv, content string) int {
	n := 0
	for _, req := range env.llmRequests() {
		for _, post := range req.Posts {
			found := strings.Contains(post.Message, content)
			for _, tc := range post.ToolUse {
				found = found || strings.Contains(tc.Result, content)
			}
			if found {
				n++
				break
			}
		}
	}
	return n
}

// TestTurnEntryPointShareReview pins that in a channel, every entry point
// that starts or resumes a turn stops at a tool result whose sources the
// channel's members may not be able to read: the result waits for the
// requester's share decision and no completion sees it first. DM turns use
// the result right away.
func TestTurnEntryPointShareReview(t *testing.T) {
	for _, entry := range turnEntryPoints {
		if entry.name == "search" {
			continue
		}
		t.Run(entry.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(),
				dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall}),
				dmMakeTextStream("It's sunny"),
			)
			env.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAutoRunEverywhere)
			if entry.dm {
				env.checker.setPolicy(mcp.EmbeddedClientKey, policyTimeToolName, mcp.ToolPolicyAutoRunInDM)
			}
			for i := range env.mcpMgr.tools {
				env.mcpMgr.tools[i].Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
					env.executions.Add(1)
					return reviewedToolResult, nil
				}
			}

			require.NoError(t, entry.seed(t, env)())
			env.waitForStream(t)

			results := storedToolResults(env, reviewedToolResult)
			require.NotEmpty(t, results, "the entry point runs a tool")
			for _, result := range results {
				require.NotNil(t, result.Shared)
				if entry.dm {
					assert.True(t, *result.Shared)
					assert.NotNil(t, result.DecidedAt)
					assert.False(t, result.AudienceReview)
					continue
				}
				assert.False(t, *result.Shared, "the result is not shared with the channel")
				assert.Nil(t, result.DecidedAt, "the result waits for the requester's share decision")
				assert.True(t, result.AudienceReview, "the share prompt warns the requester")
			}
			if entry.dm {
				assert.Positive(t, requestsMentioning(env, reviewedToolResult), "a DM reply uses the result")
				return
			}
			assert.Zero(t, requestsMentioning(env, reviewedToolResult), "no completion sees the result before the share decision")
		})
	}
}

// TestChannelMentionShareReviewFollowsSources pins which embedded tool reads
// a channel reply may use without a share decision, through a channel
// mention: only reads of channels every member of the channel can read.
func TestChannelMentionShareReviewFollowsSources(t *testing.T) {
	sales := &model.Channel{Id: "arsales-channel-id", Name: "sales", DisplayName: "Sales", Type: model.ChannelTypePrivate, TeamId: autoReplyTeamID}
	offTopic := &model.Channel{Id: "aroff-topic-id", Name: "off-topic", DisplayName: "Off-Topic", Type: model.ChannelTypeOpen, TeamId: autoReplyTeamID}
	errStats := errors.New("database unavailable")

	tests := []struct {
		name        string
		sources     func(env *policyEnv) []*model.Channel
		guests      int64
		guestErr    error
		wantReview  bool
		wantSources []llm.SourceChannel
	}{
		{
			name:    "the channel itself",
			sources: func(env *policyEnv) []*model.Channel { return []*model.Channel{env.channel} },
			guests:  3,
		},
		{
			name:    "a public channel of the team when the channel has no guests",
			sources: func(*policyEnv) []*model.Channel { return []*model.Channel{offTopic} },
		},
		{
			name:        "a public channel of the team when the channel has guests",
			sources:     func(*policyEnv) []*model.Channel { return []*model.Channel{offTopic} },
			guests:      1,
			wantReview:  true,
			wantSources: []llm.SourceChannel{{ID: offTopic.Id, Name: "off-topic", DisplayName: "Off-Topic"}},
		},
		{
			name:        "a public channel of the team when guests cannot be counted",
			sources:     func(*policyEnv) []*model.Channel { return []*model.Channel{offTopic} },
			guestErr:    errStats,
			wantReview:  true,
			wantSources: []llm.SourceChannel{{ID: offTopic.Id, Name: "off-topic", DisplayName: "Off-Topic"}},
		},
		{
			name:        "a private channel",
			sources:     func(env *policyEnv) []*model.Channel { return []*model.Channel{env.channel, sales} },
			wantReview:  true,
			wantSources: []llm.SourceChannel{{ID: sales.Id, Name: "sales", DisplayName: "Sales"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(),
				dmMakeToolCallStream([]llm.ToolCall{readChannelCall}),
				dmMakeTextStream("Here is what the channel says"),
			)
			env.mcpMgr.tools = append(env.mcpMgr.tools, sourceReportingTool("read_channel", reviewedToolResult, tc.sources(env)...))
			env.checker.setPolicy(mcp.EmbeddedClientKey, "read_channel", mcp.ToolPolicyAutoRunEverywhere)
			var countedChannel string
			env.builder.SetDestinationGuestCount(func(channelID string) (int64, error) {
				countedChannel = channelID
				return tc.guests, tc.guestErr
			})

			require.NoError(t, postMessage(env, env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" what did the channel decide"))())
			env.waitForStream(t)

			assert.Equal(t, env.channel.Id, countedChannel, "guests are counted in the reply's channel")
			results := storedToolResults(env, reviewedToolResult)
			require.Len(t, results, 1)
			result := results[0]
			require.NotNil(t, result.Shared)
			if !tc.wantReview {
				assert.True(t, *result.Shared)
				assert.NotNil(t, result.DecidedAt)
				assert.False(t, result.AudienceReview)
				assert.Equal(t, 1, requestsMentioning(env, reviewedToolResult), "the reply uses the result")
				assert.Equal(t, []string{"Here is what the channel says"}, env.streaming.streamedTexts())
				return
			}
			assert.False(t, *result.Shared)
			assert.Nil(t, result.DecidedAt)
			assert.True(t, result.AudienceReview)
			assert.Equal(t, tc.wantSources, result.SourceChannels, "the requester's share prompt names the channels")
			assert.Zero(t, requestsMentioning(env, reviewedToolResult))
			assert.Equal(t, []string{""}, env.streaming.streamedTexts(), "nothing is posted to the channel")
		})
	}
}

// TestPausedRunResumesOnShareDecision pins that a run paused for a share
// decision resumes on the requester's click: the reply may keep calling
// tools, sees the result only when shared, and its text is posted as the
// model wrote it.
func TestPausedRunResumesOnShareDecision(t *testing.T) {
	const answer = "Sales moved the launch to Friday."

	tests := []struct {
		name       string
		share      bool
		wantResult bool
	}{
		{name: "share", share: true, wantResult: true},
		{name: "keep private", share: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(),
				dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall}),
				dmMakeToolCallStream([]llm.ToolCall{readChannelCall}),
				dmMakeTextStream(answer),
			)
			env.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAutoRunEverywhere)
			env.checker.setPolicy(mcp.EmbeddedClientKey, "read_channel", mcp.ToolPolicyAutoRunEverywhere)
			env.mcpMgr.tools[0].Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				return reviewedToolResult, nil
			}
			env.mcpMgr.tools = append(env.mcpMgr.tools, sourceReportingTool("read_channel", "channel notes", env.channel))

			root := env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" what is the weather")
			env.mmClient.posts[root.Id] = root
			require.NoError(t, postMessage(env, root)())
			env.waitForStream(t)
			require.Len(t, env.llmRequests(), 1, "the run pauses after the round")

			responsePost := env.mmClient.createdPosts[len(env.mmClient.createdPosts)-1]
			env.mmClient.posts[responsePost.Id] = responsePost
			convID := finalizePausedRun(t, env, responsePost)
			responsePost.AddProp(streaming.ConversationIDProp, convID)

			var accepted []string
			if tc.share {
				accepted = []string{policyWeatherCall.ID}
			}
			require.NoError(t, env.conversations.HandleToolResult(context.Background(), autoReplyUserID, responsePost, env.channel, accepted))
			env.waitForStream(t)

			requests := env.llmRequests()
			require.Len(t, requests, 3, "the resumed reply runs another tool round, then answers")
			if tc.wantResult {
				assert.Equal(t, 2, requestsMentioning(env, reviewedToolResult), "the shared result reaches the reply")
			} else {
				assert.Zero(t, requestsMentioning(env, reviewedToolResult), "a kept-private result never reaches the reply")
			}
			assert.Equal(t, []string{"", answer}, env.streaming.streamedTexts(), "the reply is posted as written")

			weather := storedToolResults(env, reviewedToolResult)
			require.Len(t, weather, 1)
			assert.NotNil(t, weather[0].DecidedAt)
			assert.Equal(t, tc.share, *weather[0].Shared)
			notes := storedToolResults(env, "channel notes")
			require.Len(t, notes, 1)
			assert.True(t, *notes[0].Shared, "a read of the channel itself needs no decision")

			_, turnsByConv := env.convStore.snapshot()
			var order []string
			for _, turn := range turnsByConv[convID] {
				blocks, err := conversation.UnmarshalBlocks(turn.Content)
				require.NoError(t, err)
				for _, b := range blocks {
					switch b.Type {
					case conversation.BlockTypeToolUse:
						order = append(order, "use:"+b.ID)
					case conversation.BlockTypeToolResult:
						order = append(order, "result:"+b.ToolUseID)
					}
				}
			}
			assert.Equal(t, []string{
				"use:" + policyWeatherCall.ID, "result:" + policyWeatherCall.ID,
				"use:" + readChannelCall.ID, "result:" + readChannelCall.ID,
			}, order, "each round is stored before the next")
		})
	}
}

// finalizePausedRun stores the response post's own turn the way the post
// streamer does when the paused run's stream ends, and returns the
// conversation's ID.
func finalizePausedRun(t *testing.T, env *policyEnv, responsePost *model.Post) string {
	t.Helper()
	convs, turnsByConv := env.convStore.snapshot()
	require.Len(t, convs, 1)
	var convID string
	for id := range convs {
		convID = id
	}
	require.True(t, slices.ContainsFunc(turnsByConv[convID], func(turn store.Turn) bool {
		return turn.RunPostID != nil && *turn.RunPostID == responsePost.Id
	}), "the paused round belongs to the response post")
	postID := responsePost.Id
	require.NoError(t, env.convStore.CreateTurnAutoSequence(&store.Turn{
		ID:             model.NewId(),
		ConversationID: convID,
		PostID:         &postID,
		Role:           "assistant",
		Content:        json.RawMessage(`[]`),
		Status:         store.TurnStatusComplete,
	}))
	return convID
}

// TestToolDecisionAutoRunFollowsSources pins that a tool the approval click
// runs by policy follows the same rule as a run: a read of the channel itself
// is shared and the reply continues, and a read of another private channel
// waits for the requester's share decision.
func TestToolDecisionAutoRunFollowsSources(t *testing.T) {
	sales := &model.Channel{Id: "arsales-channel-id", Name: "sales", DisplayName: "Sales", Type: model.ChannelTypePrivate, TeamId: autoReplyTeamID}

	tests := []struct {
		name       string
		source     func(env *policyEnv) *model.Channel
		wantReview bool
	}{
		{name: "the channel itself", source: func(env *policyEnv) *model.Channel { return env.channel }},
		{name: "a private channel", source: func(*policyEnv) *model.Channel { return sales }, wantReview: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(), dmMakeTextStream("done"))
			env.mcpMgr.tools = append(env.mcpMgr.tools, sourceReportingTool("read_channel", reviewedToolResult, tc.source(env)))
			env.checker.setPolicy(mcp.EmbeddedClientKey, "read_channel", mcp.ToolPolicyAutoRunEverywhere)

			marked := []conversation.ContentBlock{{
				Type:             conversation.BlockTypeToolUse,
				ID:               readChannelCall.ID,
				Name:             readChannelCall.Name,
				ServerOrigin:     readChannelCall.ServerOrigin,
				Input:            readChannelCall.Arguments,
				Status:           conversation.StatusPending,
				WouldAutoExecute: true,
				Shared:           new(false),
			}}
			botPost := env.seedRun(t, env.channel, nil, humanPost("what did we decide"), append(marked, pendingQuestionBlock()...), nil)
			answers := map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"NYC"}}}

			require.NoError(t, env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.channel, []string{"q-1"}, answers))
			env.waitForStream(t)

			results := storedToolResults(env, reviewedToolResult)
			require.Len(t, results, 1)
			if !tc.wantReview {
				assert.True(t, *results[0].Shared)
				assert.NotNil(t, results[0].DecidedAt)
				assert.Equal(t, 1, requestsMentioning(env, reviewedToolResult), "the reply continues with the result")
				return
			}
			assert.False(t, *results[0].Shared)
			assert.Nil(t, results[0].DecidedAt)
			assert.True(t, results[0].AudienceReview)
			assert.Equal(t, []llm.SourceChannel{{ID: sales.Id, Name: "sales", DisplayName: "Sales"}}, results[0].SourceChannels)
			assert.Empty(t, env.llmRequests(), "no reply before the share decision")
		})
	}
}
