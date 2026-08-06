// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package north

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// BridgeToolName is the built-in tool that gives hybrid North agents access
// to their North-hosted tools. North rejects requests mixing hosted and
// function tools in one call (CANNOT_MIX_CUSTOM_AND_MANAGED_TOOLS), so when
// the Mattermost tool catalog is forwarded as function tools, hosted
// capabilities are reached through a nested, hosted-tools-only chat call
// performed by this tool's resolver.
//
// The tool is part of the regular built-in catalog (see mmtools) rather than
// being injected at request time by the provider: tool execution can happen
// outside the original request (e.g. the tool-approval resume flow rebuilds
// the tool store), so the tool must be resolvable from every catalog build.
const BridgeToolName = "north_agent_task"

// hostedToolsCacheTTL bounds how long a North agent's hosted-tool list is
// cached. Catalog builds happen per request, so lookups must not hit the
// North API every time; a short TTL still picks up admin changes reasonably
// quickly.
const hostedToolsCacheTTL = 5 * time.Minute

type hostedToolsEntry struct {
	names     []string
	fetchedAt time.Time
}

// hostedToolsCache caches hosted-tool names per baseURL+agentID.
var hostedToolsCache sync.Map

type northAgentTaskArgs struct {
	Task string `json:"task" jsonschema:"A fully self-contained task for the North agent, including all context it needs."`
}

// BridgeToolForBot returns the north_agent_task tool for a North-backed bot,
// or nil when the bridge does not apply: the service is not a North service,
// no agent ID is configured (the instance default agent cannot be introspected),
// or the North agent has no hosted tools.
func BridgeToolForBot(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig) *llm.Tool {
	if serviceConfig.Type != llm.ServiceTypeNorth {
		return nil
	}
	agentID := botConfig.Model
	if agentID == "" {
		agentID = serviceConfig.DefaultModel
	}
	if agentID == "" {
		return nil
	}

	client := NewClient(
		serviceConfig.APIURL,
		serviceConfig.APIKey,
		time.Duration(serviceConfig.StreamingTimeoutSeconds)*time.Second,
	)

	hostedNames := cachedHostedToolNames(client, agentID)
	if len(hostedNames) == 0 {
		return nil
	}

	tool := newBridgeTool(client, agentID, hostedNames)
	return &tool
}

// cachedHostedToolNames returns the hosted-tool names of a North agent,
// caching successful lookups per baseURL+agentID for hostedToolsCacheTTL.
func cachedHostedToolNames(client *Client, agentID string) []string {
	key := client.baseURL + "|" + agentID
	if value, ok := hostedToolsCache.Load(key); ok {
		entry := value.(hostedToolsEntry)
		if time.Since(entry.fetchedAt) < hostedToolsCacheTTL {
			return entry.names
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agent, err := client.GetAgent(ctx, agentID)
	if err != nil {
		// Not cached: a transient failure should not disable the bridge for
		// the full TTL.
		return nil
	}

	names := make([]string, 0, len(agent.Tools))
	for _, tool := range agent.Tools {
		if tool.NorthTool != nil && tool.NorthTool.Name != "" {
			names = append(names, tool.NorthTool.Name)
		}
	}
	hostedToolsCache.Store(key, hostedToolsEntry{names: names, fetchedAt: time.Now()})
	return names
}

// newBridgeTool builds the north_agent_task tool bound to a North agent.
func newBridgeTool(client *Client, agentID string, hostedToolNames []string) llm.Tool {
	return llm.Tool{
		Name: BridgeToolName,
		Description: fmt.Sprintf(
			"Delegate a task to the Cohere North agent, which executes it server-side using its own tools (%s). "+
				"Use this for anything that needs live web information, fetching/scraping a URL, or running code/data analysis. "+
				"The task must be fully self-contained: include all relevant context, since the North agent cannot see this conversation.",
			strings.Join(hostedToolNames, ", "),
		),
		Schema: llm.NewJSONSchemaFromStruct[northAgentTaskArgs](),
		Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			var args northAgentTaskArgs
			if err := argsGetter(&args); err != nil {
				return "", fmt.Errorf("failed to get north_agent_task arguments: %w", err)
			}
			if strings.TrimSpace(args.Task) == "" {
				return "", errors.New("task must not be empty")
			}
			return runAgentTask(ctx, client, agentID, args.Task)
		},
	}
}

// runAgentTask performs the nested hosted-tools-only North call backing the
// bridge tool and formats the result (answer text plus source URLs).
func runAgentTask(ctx context.Context, client *Client, agentID, task string) (string, error) {
	response, err := client.Chat(ctx, ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: task}},
		Stateless: true,
		Agent:     &AgentRef{ID: agentID},
		// Tools omitted on purpose: the agent's hosted tools run server-side.
	})
	if err != nil {
		return "", err
	}
	if response.Error != nil {
		return "", response.Error
	}

	var text strings.Builder
	var sources []string
	seenSources := make(map[string]bool)
	for _, message := range response.Messages {
		if message.Role != "" && message.Role != "assistant" {
			continue
		}
		for _, item := range message.ContentItems() {
			if item.Type == "text" {
				text.WriteString(item.Text)
			}
		}
		for _, citation := range message.Citations {
			url, title := citationSourceURL(citation)
			if url == "" || seenSources[url] {
				continue
			}
			seenSources[url] = true
			if title != "" {
				sources = append(sources, fmt.Sprintf("- %s: %s", title, url))
			} else {
				sources = append(sources, "- "+url)
			}
		}
	}
	if text.Len() == 0 {
		return "", errors.New("north agent returned no text")
	}
	if len(sources) > 0 {
		text.WriteString("\n\nSources:\n")
		text.WriteString(strings.Join(sources, "\n"))
	}
	return text.String(), nil
}
