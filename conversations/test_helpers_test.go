// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeStreamingService struct {
	streamedPosts []*model.Post
}

func (s *fakeStreamingService) StreamToNewPost(_ context.Context, _ string, _ string, _ *llm.TextStreamResult, post *model.Post, _ string) error {
	s.streamedPosts = append(s.streamedPosts, post.Clone())
	return nil
}

func (s *fakeStreamingService) StreamToNewDM(context.Context, string, *llm.TextStreamResult, string, *model.Post, string) error {
	return nil
}

func (s *fakeStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *fakeStreamingService) StopStreaming(string) {}

func (s *fakeStreamingService) GetStreamingContext(inCtx context.Context, _ string) (context.Context, error) {
	return inCtx, nil
}

func (s *fakeStreamingService) FinishStreaming(string) {}

type testToolProvider struct {
	tools []llm.Tool
}

func (p *testToolProvider) GetTools(_ *bots.Bot) []llm.Tool {
	return p.tools
}

// testToolCallingConfig implements conversations.ConfigProvider for testing
type testToolCallingConfig struct {
	enableChannelMentionToolCalling bool
}

func (c *testToolCallingConfig) EnableChannelMentionToolCalling() bool {
	return c.enableChannelMentionToolCalling
}

func (c *testToolCallingConfig) AllowNativeWebSearchInChannels() bool {
	return false
}

func (c *testToolCallingConfig) MCP() mcp.Config {
	return mcp.Config{}
}
