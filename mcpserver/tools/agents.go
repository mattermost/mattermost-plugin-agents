// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/format"
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
			Name:        "list_agents",
			Description: `List all available AI agents (bots). Returns each agent's ID, display name, and username.`,
			Schema:      NewJSONSchemaForAccessMode[ListAgentsArgs](string(p.accessMode)),
			Resolver:    typed("list_agents", p.toolListAgents),
		},
	}
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
