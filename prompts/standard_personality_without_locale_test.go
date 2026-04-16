// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package prompts_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

var (
	horizontalWhitespaceRun      = regexp.MustCompile(`[^\S\r\n]{2,}`)
	trailingHorizontalWhitespace = regexp.MustCompile(`(?m)[^\S\r\n]+$`)
	// Paragraph spacing uses a single blank line (\n\n), so we only disallow 3+ consecutive newlines.
	newlineRun = regexp.MustCompile(`\n{3,}`)
)

func TestStandardPersonalityWithoutLocaleWhitespaceGating(t *testing.T) {
	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	buildToolStore := func(name string) *llm.ToolStore {
		store := llm.NewToolStore(nil, false)
		store.AddTools([]llm.Tool{{
			Name:        name,
			Description: "test tool",
			Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "", nil
			},
		}})
		return store
	}

	toolModes := []struct {
		name  string
		tools *llm.ToolStore
	}{
		{name: "tools_nil", tools: nil},
		{name: "tools_without_websearch", tools: buildToolStore("ReadPost")},
		{name: "tools_with_websearch", tools: buildToolStore("WebSearch")},
	}
	channelModes := []struct {
		name    string
		channel *model.Channel
	}{
		{name: "channel_nil", channel: nil},
		{name: "channel_direct", channel: &model.Channel{Type: "D"}},
		{name: "channel_public", channel: &model.Channel{Type: "O", Name: "town-square", DisplayName: "Town Square"}},
	}
	requestingUserModes := []struct {
		name                string
		buildRequestingUser func() *model.User
	}{
		{name: "requesting_user_set", buildRequestingUser: func() *model.User { return &model.User{Username: "requester"} }},
		{name: "requesting_user_nil", buildRequestingUser: func() *model.User { return nil }},
	}
	mutators := []func(*llm.Context){
		func(c *llm.Context) { c.CompanyName = "Mattermost" },
		func(c *llm.Context) {
			c.DisabledToolsInfo = []llm.ToolInfo{{Name: "Jira", Description: "Read tickets"}}
		},
		func(c *llm.Context) { c.CustomInstructions = "Be concise." },
		func(c *llm.Context) {
			if c.RequestingUser != nil {
				c.RequestingUser.FirstName = "Pat"
			}
		},
		func(c *llm.Context) {
			if c.RequestingUser != nil {
				c.RequestingUser.LastName = "Lee"
			}
		},
		func(c *llm.Context) {
			if c.RequestingUser != nil {
				c.RequestingUser.Position = "Engineer"
			}
		},
		func(c *llm.Context) { c.Team = &model.Team{Name: "eng", DisplayName: "Engineering"} },
	}

	for _, toolMode := range toolModes {
		for _, channelMode := range channelModes {
			for _, requestingUserMode := range requestingUserModes {
				for flags := 0; flags < 1<<len(mutators); flags++ {
					context := &llm.Context{
						Time:           "Fri, 20 Feb 2026 18:00:00 UTC",
						ServerName:     "server",
						BotName:        "agent",
						BotUsername:    "agent",
						BotModel:       "model-x",
						Tools:          toolMode.tools,
						RequestingUser: requestingUserMode.buildRequestingUser(),
						Channel:        channelMode.channel,
					}

					for i, mutate := range mutators {
						if flags&(1<<i) != 0 {
							mutate(context)
						}
					}

					label := fmt.Sprintf("tools=%s channel=%s requesting_user=%s flags=%0*b", toolMode.name, channelMode.name, requestingUserMode.name, len(mutators), flags)
					output, err := promptsEngine.Format(prompts.PromptStandardPersonalityWithoutLocale, context)
					require.NoError(t, err, label)
					require.Falsef(t, horizontalWhitespaceRun.MatchString(output), "%s contains repeated horizontal whitespace", label)
					require.Falsef(t, trailingHorizontalWhitespace.MatchString(output), "%s contains trailing horizontal whitespace", label)
					require.Falsef(t, newlineRun.MatchString(output), "%s contains repeated newline runs", label)
				}
			}
		}
	}
}
