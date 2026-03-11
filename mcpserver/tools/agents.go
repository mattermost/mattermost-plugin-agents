// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

const aiBotsAPIPath = "/plugins/mattermost-ai/ai_bots"

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

// getAgentTools returns agent discovery tools.
func (p *MattermostToolProvider) getAgentTools() []MCPTool {
	return []MCPTool{
		{
			Name: "list_agents",
			Description: `List all available AI agents (bots) and LLM services. Returns each agent's ID, display name, username, and service type.
Use this tool to discover valid provider_id values for the ai_prompt action type when creating automations.
The agent ID (26-character Mattermost user ID) is what you pass as config.provider_id with provider_type "agent".`,
			Schema:   llm.NewJSONSchemaFromStruct[ListAgentsArgs](),
			Resolver: p.toolListAgents,
		},
	}
}

// toolListAgents fetches available agents via the plugin's /ai_bots endpoint.
func (p *MattermostToolProvider) toolListAgents(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args ListAgentsArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool list_agents: %w", err)
	}

	bots, err := p.fetchAIBots(mcpContext.Client)
	if err != nil {
		return "Failed to retrieve agents. The AI plugin is not reachable.", fmt.Errorf("failed to fetch agents: %w", err)
	}

	if len(bots) == 0 {
		return "No agents are currently configured.", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d agent(s):\n\n", len(bots)))

	for i, a := range bots {
		result.WriteString(fmt.Sprintf("%d. %s\n", i+1, a.DisplayName))
		result.WriteString(fmt.Sprintf("   ID: %s\n", a.ID))
		result.WriteString(fmt.Sprintf("   Username: @%s\n", a.Username))
		if mcpContext.BotUserID != "" && a.ID == mcpContext.BotUserID {
			result.WriteString("   ** This is YOU (the current agent) **\n")
		}
		result.WriteString("\n")
	}

	result.WriteString("Use the agent ID as config.provider_id with provider_type \"agent\" in automation actions.")

	return result.String(), nil
}

// fetchAIBots calls the plugin's /ai_bots endpoint using the authenticated Client4.
// The Mattermost server authenticates the Bearer token and sets Mattermost-User-Id,
// which satisfies the plugin's MattermostAuthorizationRequired middleware.
func (p *MattermostToolProvider) fetchAIBots(client *model.Client4) ([]AIBotInfo, error) {
	url := p.mmInternalServerURL + aiBotsAPIPath

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
