// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/delegation"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

const aiBotsAPIPath = "/plugins/mattermost-ai/ai_bots"

// maxDelegationTaskLength bounds the task text an agent can delegate.
const maxDelegationTaskLength = 8000

// DelegationService executes agent-to-agent delegations. The plugin's
// delegation service implements this for embedded servers; external
// HTTP/stdio servers pass nil, which hides the ask_agent tool.
type DelegationService interface {
	// Delegate runs the delegation and returns the model-visible result text.
	// On failure the returned error text is the model-visible guidance.
	Delegate(ctx context.Context, req delegation.Request) (string, error)

	// Available reports whether the service is fully configured.
	Available() bool
}

// AIBotInfo mirrors the api.AIBotInfo type for the fields we need.
type AIBotInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Username    string `json:"username"`
}

// AIBotsResponse mirrors the api.AIBotsResponse type.
type AIBotsResponse struct {
	Bots []AIBotInfo `json:"bots"`
}

// ListAgentsArgs represents arguments for the list_agents tool.
type ListAgentsArgs struct{}

// AskAgentArgs represents arguments for the ask_agent tool.
type AskAgentArgs struct {
	Agent string `json:"agent" jsonschema:"Username (with or without a leading @) or bot user ID of the target agent"`
	Task  string `json:"task" jsonschema:"Self-contained task description for the target agent. Include all necessary context; the target agent cannot see this conversation."`
}

const askAgentDescription = `Delegate a task to another AI agent on behalf of the user and return its answer.

Use this when another agent is better suited for the task (different expertise, instructions, knowledge, or configuration). Answer directly instead of delegating when you can handle the request yourself.

The task must be fully self-contained: the target agent cannot see this conversation, so include every detail it needs. Use list_agents to discover available agents. The delegated work runs as the requesting user in a visible thread in their direct-message channel with the target agent; the target agent's final answer is returned as this tool's result, with a permalink to that thread. The target agent may take a while or ask the user for input, so the call can take some time to return.`

// getAgentTools returns agent discovery and delegation tools.
func (p *MattermostToolProvider) getAgentTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "list_agents",
			Description: `List all available AI agents (bots). Returns each agent's ID, display name, and username.`,
			Schema:      NewJSONSchemaForAccessMode[ListAgentsArgs](string(p.accessMode)),
			Resolver:    typed("list_agents", p.toolListAgents),
		},
		{
			Name:        "ask_agent",
			Description: askAgentDescription,
			Schema:      NewJSONSchemaForAccessMode[AskAgentArgs](string(p.accessMode)),
			Resolver:    typed("ask_agent", p.toolAskAgent),
			Available:   p.delegationAvailable,
		},
	}
}

// delegationAvailable gates ask_agent visibility on a wired delegation service.
func (p *MattermostToolProvider) delegationAvailable() bool {
	return p.delegationService != nil && p.delegationService.Available()
}

// toolAskAgent delegates a task to another agent via the delegation service.
// The initiator identity always comes from the authenticated MCP session and
// the delegating agent from server-injected call metadata — never from tool
// arguments.
func (p *MattermostToolProvider) toolAskAgent(mcpContext *MCPToolContext, args AskAgentArgs) (string, error) {
	if p.delegationService == nil || !p.delegationService.Available() {
		return "", fmt.Errorf("delegation is not available on this server")
	}
	if mcpContext.UserID == "" {
		return "", fmt.Errorf("delegation requires an authenticated user session")
	}
	if mcpContext.BotUserID == "" {
		return "", fmt.Errorf("delegation is only available to agents running on this server")
	}

	targetAgent := strings.TrimSpace(args.Agent)
	if targetAgent == "" {
		return "", fmt.Errorf("agent is required: provide the username or ID of the agent to delegate to (use list_agents to discover agents)")
	}
	task := strings.TrimSpace(args.Task)
	if task == "" {
		return "", fmt.Errorf("task is required: provide a self-contained description of the task to delegate")
	}
	if taskLength := utf8.RuneCountInString(task); taskLength > maxDelegationTaskLength {
		return "", fmt.Errorf("task is too long (%d characters, maximum %d): shorten the task description", taskLength, maxDelegationTaskLength)
	}

	return p.delegationService.Delegate(mcpContext.Ctx, delegation.Request{
		InitiatorUserID:     mcpContext.UserID,
		DelegatingBotUserID: mcpContext.BotUserID,
		TargetAgent:         targetAgent,
		Task:                task,
		ParentToolCallID:    mcpContext.ParentToolCallID,
	})
}

// toolListAgents fetches available agents via the plugin's /ai_bots endpoint.
func (p *MattermostToolProvider) toolListAgents(mcpContext *MCPToolContext, _ ListAgentsArgs) (string, error) {
	bots, err := p.fetchAIBots(mcpContext.Client)
	if err != nil {
		return "", fmt.Errorf("failed to fetch agents: %w", err)
	}

	if len(bots) == 0 {
		return "No agents are currently configured.", nil
	}

	infos := make([]format.AgentInfo, len(bots))
	for i := range bots {
		infos[i] = format.AgentInfo{
			ID:          bots[i].ID,
			DisplayName: bots[i].DisplayName,
			Username:    bots[i].Username,
		}
	}
	return format.AgentList(infos, mcpContext.BotUserID), nil
}

// fetchAIBots calls the plugin's /ai_bots endpoint using the authenticated Client4.
// The Mattermost server authenticates the Bearer token and sets Mattermost-User-Id,
// which satisfies the plugin's MattermostAuthorizationRequired middleware.
func (p *MattermostToolProvider) fetchAIBots(client *model.Client4) ([]AIBotInfo, error) {
	url := p.mmServerURL + aiBotsAPIPath

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set(model.HeaderAuth, model.HeaderBearer+" "+client.AuthToken)

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach AI plugin: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AI plugin returned status %d", resp.StatusCode)
	}

	var botsResp AIBotsResponse
	if err := json.NewDecoder(resp.Body).Decode(&botsResp); err != nil {
		return nil, fmt.Errorf("failed to decode bots response: %w", err)
	}

	return botsResp.Bots, nil
}
