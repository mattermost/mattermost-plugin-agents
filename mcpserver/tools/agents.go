// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

const aiPluginBridgeAPIPath = "/plugins/mattermost-ai/bridge/v1"

// BridgeAgentInfo mirrors the bridgeclient.BridgeAgentInfo type.
type BridgeAgentInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Username    string `json:"username"`
	ServiceID   string `json:"service_id"`
	ServiceType string `json:"service_type"`
	IsDefault   bool   `json:"is_default"`
}

// BridgeAgentsResponse mirrors the bridgeclient.AgentsResponse type.
type BridgeAgentsResponse struct {
	Agents []BridgeAgentInfo `json:"agents"`
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

// toolListAgents fetches available agents from the AI plugin bridge API.
func (p *MattermostToolProvider) toolListAgents(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args ListAgentsArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool list_agents: %w", err)
	}

	agents, err := p.fetchBridgeAgents()
	if err != nil {
		return "Failed to retrieve agents. The AI plugin bridge API is not reachable.", fmt.Errorf("failed to fetch agents: %w", err)
	}

	if len(agents) == 0 {
		return "No agents are currently configured.", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d agent(s):\n\n", len(agents)))

	// Note which agent is "self" if we know the bot user ID
	for i, a := range agents {
		result.WriteString(fmt.Sprintf("%d. %s\n", i+1, a.DisplayName))
		result.WriteString(fmt.Sprintf("   ID: %s\n", a.ID))
		result.WriteString(fmt.Sprintf("   Username: @%s\n", a.Username))
		result.WriteString(fmt.Sprintf("   Service Type: %s\n", a.ServiceType))
		if a.IsDefault {
			result.WriteString("   (default agent)\n")
		}
		if mcpContext.BotUserID != "" && a.ID == mcpContext.BotUserID {
			result.WriteString("   ** This is YOU (the current agent) **\n")
		}
		result.WriteString("\n")
	}

	result.WriteString("Use the agent ID as config.provider_id with provider_type \"agent\" in automation actions.")

	return result.String(), nil
}

// fetchBridgeAgents makes a direct HTTP call to the AI plugin's bridge API.
// This uses the Mattermost-Plugin-ID header for inter-plugin auth.
func (p *MattermostToolProvider) fetchBridgeAgents() ([]BridgeAgentInfo, error) {
	url := p.mmInternalServerURL + aiPluginBridgeAPIPath + "/agents"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Mattermost-Plugin-ID", "mattermost-ai")

	resp, err := http.DefaultClient.Do(req) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to reach bridge API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bridge API returned status %d", resp.StatusCode)
	}

	var agentsResp BridgeAgentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&agentsResp); err != nil {
		return nil, fmt.Errorf("failed to decode agents response: %w", err)
	}

	return agentsResp.Agents, nil
}
