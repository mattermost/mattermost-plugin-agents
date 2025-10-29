// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetAgents retrieves all available agents from the bridge API.
// If userID is provided, only agents accessible to that user are returned.
func (c *Client) GetAgents(userID string) ([]BridgeAgentInfo, error) {
	url := fmt.Sprintf("/%s/bridge/v1/agents", aiPluginID)
	if userID != "" {
		url = fmt.Sprintf("%s?user_id=%s", url, userID)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
		}
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, errResp.Error)
	}

	var agentsResp AgentsResponse
	if err := json.Unmarshal(respBody, &agentsResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return agentsResp.Agents, nil
}

// GetServices retrieves all available services from the bridge API.
// If userID is provided, only services accessible to that user (via their permitted bots) are returned.
func (c *Client) GetServices(userID string) ([]BridgeServiceInfo, error) {
	url := fmt.Sprintf("/%s/bridge/v1/services", aiPluginID)
	if userID != "" {
		url = fmt.Sprintf("%s?user_id=%s", url, userID)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
		}
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, errResp.Error)
	}

	var servicesResp ServicesResponse
	if err := json.Unmarshal(respBody, &servicesResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return servicesResp.Services, nil
}
