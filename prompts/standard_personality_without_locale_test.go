// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package prompts_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
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

	buildToolStore := func(names ...string) *llm.ToolStore {
		store := llm.NewToolStore()
		tools := make([]llm.Tool, 0, len(names))
		for _, name := range names {
			tools = append(tools, llm.Tool{
				Name:        name,
				Description: "test tool",
				Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
					return "", nil
				},
			})
		}
		store.AddTools(tools)
		return store
	}

	toolModes := []struct {
		name                  string
		tools                 *llm.ToolStore
		mcpDynamicToolLoading bool
		unconnected           []llm.UnconnectedMCPServer
	}{
		{name: "tools_nil", tools: nil},
		{name: "tools_without_websearch", tools: buildToolStore("ReadPost")},
		{name: "tools_with_websearch", tools: buildToolStore("WebSearch")},
		{name: "tools_with_dynamic_mcp_meta_tools", tools: buildToolStore("search_tools", "load_tool"), mcpDynamicToolLoading: true},
		{name: "tools_with_unconnected_mcp_servers", tools: buildToolStore("ReadPost", llm.ConnectMCPServerToolName), unconnected: testUnconnectedMCPServers()},
		{name: "tools_with_dynamic_unconnected_mcp_servers", tools: buildToolStore("search_tools", "load_tool", llm.ConnectMCPServerToolName), mcpDynamicToolLoading: true, unconnected: testUnconnectedMCPServers()},
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
						ToolCatalog:    llm.ToolCatalogContext{MCPDynamicToolLoading: toolMode.mcpDynamicToolLoading, UnconnectedMCPServers: toolMode.unconnected},
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
					if !toolMode.mcpDynamicToolLoading {
						require.Falsef(t, horizontalWhitespaceRun.MatchString(output), "%s contains repeated horizontal whitespace", label)
					}
					require.Falsef(t, trailingHorizontalWhitespace.MatchString(output), "%s contains trailing horizontal whitespace", label)
					require.Falsef(t, newlineRun.MatchString(output), "%s contains repeated newline runs", label)
				}
			}
		}
	}
}

func TestStandardPersonalityWithoutLocaleListsAvailableToolsForGeminiAndVertexOnly(t *testing.T) {
	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{
		{
			Name:        "search_users",
			Description: "Look up users by name",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "", nil
			},
		},
		{
			Name:        "read_channel",
			Description: "Read channel history",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "", nil
			},
		},
	})

	buildContext := func(serviceType string) *llm.Context {
		return &llm.Context{
			Time:           "Fri, 20 Feb 2026 18:00:00 UTC",
			ServerName:     "server",
			BotName:        "agent",
			BotUsername:    "agent",
			BotModel:       "model-x",
			BotServiceType: serviceType,
			Tools:          store,
		}
	}

	geminiOutput, err := promptsEngine.Format(prompts.PromptStandardPersonalityWithoutLocale, buildContext("gemini"))
	require.NoError(t, err)
	assert.Contains(t, geminiOutput, "The tools currently available to agent in this conversation are:")
	assert.Contains(t, geminiOutput, "- search_users: Look up users by name")
	assert.Contains(t, geminiOutput, "- read_channel: Read channel history")
	assert.Contains(t, geminiOutput, "When asked about capabilities or tool access, agent may mention the tools listed above.")

	vertexOutput, err := promptsEngine.Format(prompts.PromptStandardPersonalityWithoutLocale, buildContext("vertex"))
	require.NoError(t, err)
	assert.Contains(t, vertexOutput, "The tools currently available to agent in this conversation are:")
	assert.Contains(t, vertexOutput, "- search_users: Look up users by name")
	assert.Contains(t, vertexOutput, "- read_channel: Read channel history")
	assert.Contains(t, vertexOutput, "When asked about capabilities or tool access, agent may mention the tools listed above.")

	openAIOutput, err := promptsEngine.Format(prompts.PromptStandardPersonalityWithoutLocale, buildContext("openai"))
	require.NoError(t, err)
	assert.NotContains(t, openAIOutput, "The tools currently available to agent in this conversation are:")
	assert.NotContains(t, openAIOutput, "When asked about capabilities or tool access, agent may mention the tools listed above.")
}

func TestStandardPersonalityPostPermalinkGuidance(t *testing.T) {
	tests := []struct {
		name        string
		siteURL     string
		contains    []string
		notContains []string
	}{
		{
			name:    "configured site URL",
			siteURL: "https://mattermost.example.com",
			contains: []string{
				"The Mattermost site URL is https://mattermost.example.com.",
				"https://mattermost.example.com/_redirect/pl/<post_id>",
				"including posts in private channels and direct messages",
			},
		},
		{
			name:        "site URL unavailable",
			notContains: []string{"The Mattermost site URL is", "/_redirect/pl/<post_id>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := renderStandardPersonalityWithoutLocale(t, &llm.Context{
				BotName:     "agent",
				BotUsername: "agent",
				SiteURL:     tt.siteURL,
			})
			for _, expected := range tt.contains {
				assert.Contains(t, output, expected)
			}
			for _, unexpected := range tt.notContains {
				assert.NotContains(t, output, unexpected)
			}
		})
	}
}

func TestStandardPersonalityDynamicToolWorkflow(t *testing.T) {
	dynamicWorkflowText := []string{
		"You have access to a large set of MCP tools through two meta-tools.",
		"You MUST load a tool",
		"CRITICAL: For side-effecting external actions",
	}
	tests := []struct {
		name        string
		context     *llm.Context
		contains    []string
		notContains []string
	}{
		{
			name: "includes workflow when enabled",
			context: &llm.Context{
				Time:        "Fri, 20 Feb 2026 18:00:00 UTC",
				ServerName:  "server",
				BotName:     "agent",
				BotUsername: "agent",
				BotModel:    "model-x",
				Tools:       dynamicMetaToolStore(),
				ToolCatalog: llm.ToolCatalogContext{MCPDynamicToolLoading: true},
			},
			contains: append(dynamicWorkflowText, "ask one focused clarification question"),
			notContains: []string{
				"pick a reasonable default",
			},
		},
		{
			name: "includes workflow with disabled tools info",
			context: &llm.Context{
				Time:              "Fri, 20 Feb 2026 18:00:00 UTC",
				ServerName:        "server",
				BotName:           "agent",
				BotUsername:       "agent",
				BotModel:          "model-x",
				Tools:             dynamicMetaToolStore(),
				DisabledToolsInfo: []llm.ToolInfo{{Name: "Jira", Description: "Read tickets"}},
				ToolCatalog:       llm.ToolCatalogContext{MCPDynamicToolLoading: true},
			},
			contains: append(dynamicWorkflowText,
				"ask one focused clarification question",
				"IMPORTANT: You have capabilities that can only be used in a Direct Message (DM) or via the Agents tab",
			),
			notContains: []string{
				"pick a reasonable default",
			},
		},
		{
			name: "omits workflow when meta tools are disabled",
			context: &llm.Context{
				Time:        "Fri, 20 Feb 2026 18:00:00 UTC",
				ServerName:  "server",
				BotName:     "agent",
				BotUsername: "agent",
				BotModel:    "model-x",
				Tools:       dynamicMetaToolStore(),
				DisabledToolsInfo: []llm.ToolInfo{
					{Name: "search_tools", Description: "Search tools"},
					{Name: "load_tool", Description: "Load tool"},
				},
				ToolCatalog: llm.ToolCatalogContext{MCPDynamicToolLoading: true},
			},
			contains:    []string{"IMPORTANT: You have capabilities that can only be used in a Direct Message (DM) or via the Agents tab"},
			notContains: dynamicWorkflowText,
		},
		{
			name: "omits workflow when flag off",
			context: &llm.Context{
				Time:        "Fri, 20 Feb 2026 18:00:00 UTC",
				ServerName:  "server",
				BotName:     "agent",
				BotUsername: "agent",
				BotModel:    "model-x",
				Tools:       dynamicMetaToolStore(),
			},
			notContains: dynamicWorkflowText,
		},
		{
			name: "omits workflow without meta tools",
			context: &llm.Context{
				Time:        "Fri, 20 Feb 2026 18:00:00 UTC",
				ServerName:  "server",
				BotName:     "agent",
				BotUsername: "agent",
				BotModel:    "model-x",
				Tools:       llm.NewToolStore(),
				ToolCatalog: llm.ToolCatalogContext{MCPDynamicToolLoading: true},
			},
			notContains: dynamicWorkflowText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := renderStandardPersonalityWithoutLocale(t, tt.context)
			for _, expected := range tt.contains {
				assert.Contains(t, output, expected)
			}
			for _, unexpected := range tt.notContains {
				assert.NotContains(t, output, unexpected)
			}
		})
	}
}

// Guidance renders only when this turn's sandbox output will actually attach.
// The model never sees provider file ids, so $OUTPUT_DIR is the only share
// gesture — showing it otherwise would promise attachments that never arrive.
func TestStandardPersonalitySandboxFileGuidance(t *testing.T) {
	const guidance = "$OUTPUT_DIR"

	tests := []struct {
		name    string
		context *llm.Context
		want    bool
	}{
		{
			name: "attachment active",
			context: &llm.Context{
				BotName:     "ai",
				ToolCatalog: llm.ToolCatalogContext{SandboxFilesAttached: true},
			},
			want: true,
		},
		{
			name: "attachment inactive",
			context: &llm.Context{
				BotName:     "ai",
				ToolCatalog: llm.ToolCatalogContext{ResponseFilesSupported: true},
			},
			want: false,
		},
		{
			// Sandbox still runs with Mattermost tools disabled, so guidance cannot be gated on tools.
			name: "attachment active with no tool store",
			context: &llm.Context{
				BotName:     "ai",
				Tools:       nil,
				ToolCatalog: llm.ToolCatalogContext{SandboxFilesAttached: true},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := renderStandardPersonalityWithoutLocale(t, tt.context)
			if tt.want {
				assert.Contains(t, output, guidance)
				assert.Contains(t, output, "NOT visible to the user")
			} else {
				assert.NotContains(t, output, guidance)
			}
		})
	}
}

// The system prompt is fixed when a conversation starts, so it carries only
// static guidance; the per-turn ConnectMCPServer tool schema is the one
// place unconnected servers are listed.
func TestStandardPersonalityUnconnectedMCPServers(t *testing.T) {
	const guidance = "integration listed in the ConnectMCPServer tool"
	connectTool := llm.Tool{
		Name:            llm.ConnectMCPServerToolName,
		Description:     "Connect",
		UserInteraction: llm.UserInteractionConnect,
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "", nil
		},
	}
	storeWith := func(base *llm.ToolStore, tools ...llm.Tool) *llm.ToolStore {
		base.AddTools(tools)
		return base
	}

	tests := []struct {
		name         string
		tools        *llm.ToolStore
		dynamic      bool
		disabled     []llm.ToolInfo
		wantGuidance bool
	}{
		{name: "guidance when ConnectMCPServer is callable", tools: storeWith(llm.NewToolStore(), connectTool), wantGuidance: true},
		{name: "guidance alongside the meta-tool workflow", tools: storeWith(dynamicMetaToolStore(), connectTool), dynamic: true, wantGuidance: true},
		{name: "omitted when ConnectMCPServer is not cataloged", tools: llm.NewToolStore()},
		{
			name:     "omitted when ConnectMCPServer is disabled in this context",
			tools:    storeWith(llm.NewToolStore(), connectTool),
			disabled: []llm.ToolInfo{{Name: llm.ConnectMCPServerToolName, Description: "Connect"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := renderStandardPersonalityWithoutLocale(t, &llm.Context{
				BotName:           "agent",
				Tools:             tt.tools,
				DisabledToolsInfo: tt.disabled,
				ToolCatalog: llm.ToolCatalogContext{
					MCPDynamicToolLoading: tt.dynamic,
					UnconnectedMCPServers: testUnconnectedMCPServers(),
				},
			})
			assert.Equal(t, tt.wantGuidance, strings.Contains(output, guidance))
			assert.NotContains(t, output, "Atlassian Jira issues")
			assert.NotContains(t, output, "create_issue")
			assert.NotContains(t, output, "/mcp/oauth/")
			if tt.dynamic {
				assert.Contains(t, output, "You have access to a large set of MCP tools")
			}
		})
	}
}

func testUnconnectedMCPServers() []llm.UnconnectedMCPServer {
	return []llm.UnconnectedMCPServer{
		{
			Name:        "Jira",
			Origin:      "jira",
			Description: "Atlassian Jira issues",
			AuthURL:     "https://mm.example.com/plugins/mattermost-ai/mcp/oauth/Jira/start",
			ToolNames:   []string{"create_issue", "search_issues"},
		},
		{
			Name:    "Notes",
			Origin:  "notes",
			AuthURL: "https://mm.example.com/plugins/mattermost-ai/mcp/oauth/Notes/start",
		},
	}
}

func renderStandardPersonalityWithoutLocale(t *testing.T, context *llm.Context) string {
	t.Helper()

	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	output, err := promptsEngine.Format(prompts.PromptStandardPersonalityWithoutLocale, context)
	require.NoError(t, err)
	return output
}

func dynamicMetaToolStore() *llm.ToolStore {
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{
		{
			Name:        "search_tools",
			Description: "Search tools",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "", nil
			},
		},
		{
			Name:        "load_tool",
			Description: "Load tool",
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "", nil
			},
		},
	})
	return store
}
