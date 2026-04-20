// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// provideAgentTools registers agent discovery MCP tools.
func (p *MattermostToolProvider) provideAgentTools(s *mcp.Server) {
	registerTool(s, p, "list_agents",
		`List all available AI agents (bots). Returns each agent's ID, display name, and username.`,
		llm.NewJSONSchemaFromStruct[ListAgentsArgs](),
		p.toolListAgents,
		format.ListAgentsOutput,
	)
}

// toolListAgents fetches available agents via the plugin's /ai_bots endpoint.
func (p *MattermostToolProvider) toolListAgents(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.ListAgentsOutput, error) {
	var args ListAgentsArgs
	if err := argsGetter(&args); err != nil {
		return mcptool.ListAgentsOutput{}, fmt.Errorf("failed to get arguments for tool list_agents: %w", err)
	}

	if mcpContext == nil || mcpContext.Client == nil {
		return mcptool.ListAgentsOutput{}, fmt.Errorf("client not available in context")
	}

	bots, err := p.fetchAIBots(mcpContext.Client)
	if err != nil {
		return mcptool.ListAgentsOutput{}, fmt.Errorf("failed to fetch agents: %w", err)
	}

	infos := make([]mcptool.AgentInfo, len(bots))
	for i := range bots {
		infos[i] = mcptool.AgentInfo{
			ID:          bots[i].ID,
			DisplayName: bots[i].DisplayName,
			Username:    bots[i].Username,
		}
	}
	out := mcptool.ListAgentsOutput{
		Agents:           infos,
		CurrentBotUserID: mcpContext.BotUserID,
	}
	return out, nil
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
