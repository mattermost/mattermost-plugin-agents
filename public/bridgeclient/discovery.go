// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

func (c *Client) doGetJSON(requestURL string, out any) error {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return requestFailedError(resp.StatusCode, respBody)
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

func appendValidatedUserIDQuery(requestURL string, userID string) (string, error) {
	if userID == "" {
		return requestURL, nil
	}

	if err := ValidateID(userID); err != nil {
		return "", fmt.Errorf("invalid user ID: %w", err)
	}

	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse request URL: %w", err)
	}

	query := parsedURL.Query()
	query.Set("user_id", userID)
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String(), nil
}

// GetAgents retrieves all available agents from the bridge API.
// If userID is provided, only agents accessible to that user are returned.
func (c *Client) GetAgents(userID string) ([]BridgeAgentInfo, error) {
	requestURL := fmt.Sprintf("/%s/bridge/v1/agents", AiPluginID)
	updatedRequestURL, err := appendValidatedUserIDQuery(requestURL, userID)
	if err != nil {
		return nil, err
	}
	requestURL = updatedRequestURL

	var agentsResp AgentsResponse
	if err := c.doGetJSON(requestURL, &agentsResp); err != nil {
		return nil, err
	}

	return agentsResp.Agents, nil
}

// GetServices retrieves all available services from the bridge API.
// If userID is provided, only services accessible to that user (via their permitted bots) are returned.
func (c *Client) GetServices(userID string) ([]BridgeServiceInfo, error) {
	requestURL := fmt.Sprintf("/%s/bridge/v1/services", AiPluginID)
	updatedRequestURL, err := appendValidatedUserIDQuery(requestURL, userID)
	if err != nil {
		return nil, err
	}
	requestURL = updatedRequestURL

	var servicesResp ServicesResponse
	if err := c.doGetJSON(requestURL, &servicesResp); err != nil {
		return nil, err
	}

	return servicesResp.Services, nil
}

// GetAgentTools retrieves bridge-eligible tools for a specific agent.
// If userID is provided, the result is filtered to what that user can access.
func (c *Client) GetAgentTools(agent string, userID string) ([]BridgeToolInfo, error) {
	if err := ValidateID(agent); err != nil {
		return nil, fmt.Errorf("invalid agent ID: %w", err)
	}

	requestURL := fmt.Sprintf("/%s/bridge/v1/agents/%s/tools", AiPluginID, agent)
	updatedRequestURL, err := appendValidatedUserIDQuery(requestURL, userID)
	if err != nil {
		return nil, err
	}
	requestURL = updatedRequestURL

	var toolsResp AgentToolsResponse
	if err := c.doGetJSON(requestURL, &toolsResp); err != nil {
		return nil, err
	}

	return toolsResp.Tools, nil
}
