// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

const pluginRoutePrefix = "/plugins/mattermost-ai"

type agentToolPolicyMode string

const (
	agentToolPolicyAllowAll  agentToolPolicyMode = "allow_all"
	agentToolPolicyAllowNone agentToolPolicyMode = "allow_none"
	agentToolPolicyAllowList agentToolPolicyMode = "allow_list"
)

type agentAccessLevel string

const (
	agentAccessLevelAll   agentAccessLevel = "all"
	agentAccessLevelAllow agentAccessLevel = "allow"
	agentAccessLevelBlock agentAccessLevel = "block"
	agentAccessLevelNone  agentAccessLevel = "none"
)

type serviceInfoResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type agentAPIRequest struct {
	DisplayName             string               `json:"displayName"`
	Username                string               `json:"username,omitempty"`
	ServiceID               string               `json:"serviceID"`
	CustomInstructions      string               `json:"customInstructions"`
	ChannelAccessLevel      int                  `json:"channelAccessLevel"`
	ChannelIDs              []string             `json:"channelIDs"`
	UserAccessLevel         int                  `json:"userAccessLevel"`
	UserIDs                 []string             `json:"userIDs"`
	TeamIDs                 []string             `json:"teamIDs"`
	AdminUserIDs            []string             `json:"adminUserIDs"`
	EnabledMCPTools         []llm.EnabledMCPTool `json:"enabledMCPTools"`
	Model                   string               `json:"model"`
	EnableVision            bool                 `json:"enableVision"`
	DisableTools            bool                 `json:"disableTools"`
	EnabledNativeTools      []string             `json:"enabledNativeTools"`
	ReasoningEnabled        bool                 `json:"reasoningEnabled"`
	ReasoningEffort         string               `json:"reasoningEffort"`
	ThinkingBudget          int                  `json:"thinkingBudget"`
	StructuredOutputEnabled bool                 `json:"structuredOutputEnabled"`
}

type CreateAgentToolArgs struct {
	DisplayName string `json:"display_name" jsonschema:"The new agent display name,minLength=1"`
	Username    string `json:"username" jsonschema:"The new agent username without the @ prefix. Must start with a lowercase letter and only use lowercase letters, numbers, dots, dashes, or underscores,minLength=1"`
	ServiceID   string `json:"service_id,omitempty" jsonschema:"Optional configured service ID to use for the agent"`
	ServiceName string `json:"service_name,omitempty" jsonschema:"Optional configured service name to resolve when the service ID is unknown"`

	CustomInstructions      string               `json:"custom_instructions,omitempty" jsonschema:"Optional system prompt / instructions for the agent"`
	ChannelAccessLevel      agentAccessLevel     `json:"channel_access_level,omitempty" jsonschema:"Optional channel access level: all, allow, block, or none,enum=all,enum=allow,enum=block,enum=none"`
	ChannelIDs              []string             `json:"channel_ids,omitempty" jsonschema:"Optional channel IDs used when channel_access_level is allow or block"`
	UserAccessLevel         agentAccessLevel     `json:"user_access_level,omitempty" jsonschema:"Optional user access level: all, allow, block, or none,enum=all,enum=allow,enum=block,enum=none"`
	UserIDs                 []string             `json:"user_ids,omitempty" jsonschema:"Optional user IDs used when user_access_level is allow or block"`
	TeamIDs                 []string             `json:"team_ids,omitempty" jsonschema:"Optional team IDs the agent is available in"`
	AdminUserIDs            []string             `json:"admin_user_ids,omitempty" jsonschema:"Optional user IDs that can manage the created agent"`
	EnabledMCPToolsMode     agentToolPolicyMode  `json:"enabled_mcp_tools_mode,omitempty" jsonschema:"Optional MCP tool policy. allow_all permits every MCP tool, allow_none disables all MCP tools, and allow_list restricts the agent to enabled_mcp_tools,enum=allow_all,enum=allow_none,enum=allow_list"`
	EnabledMCPTools         []llm.EnabledMCPTool `json:"enabled_mcp_tools,omitempty" jsonschema:"Optional MCP tool allowlist used when enabled_mcp_tools_mode is allow_list. Each item needs server_origin and tool_name"`
	Model                   string               `json:"model,omitempty" jsonschema:"Optional model override for the selected service"`
	EnableVision            *bool                `json:"enable_vision,omitempty" jsonschema:"Optional; defaults to true"`
	DisableTools            *bool                `json:"disable_tools,omitempty" jsonschema:"Optional; defaults to false"`
	EnabledNativeTools      []string             `json:"enabled_native_tools,omitempty" jsonschema:"Optional native provider tools. Defaults to [\"web_search\"] to match the UI"`
	ReasoningEnabled        *bool                `json:"reasoning_enabled,omitempty" jsonschema:"Optional; defaults to true"`
	ReasoningEffort         string               `json:"reasoning_effort,omitempty" jsonschema:"Optional reasoning effort for supported models. Defaults to medium"`
	ThinkingBudget          *int                 `json:"thinking_budget,omitempty" jsonschema:"Optional thinking budget for supported Anthropic models"`
	StructuredOutputEnabled *bool                `json:"structured_output_enabled,omitempty" jsonschema:"Optional; defaults to false"`
}

type UpdateAgentToolArgs struct {
	AgentID       string  `json:"agent_id,omitempty" jsonschema:"Optional agent ID to update"`
	AgentUsername string  `json:"agent_username,omitempty" jsonschema:"Optional agent username to update when the agent ID is unknown"`
	ServiceID     *string `json:"service_id,omitempty" jsonschema:"Optional replacement service ID"`
	ServiceName   *string `json:"service_name,omitempty" jsonschema:"Optional replacement service name when the service ID is unknown"`

	DisplayName             *string               `json:"display_name,omitempty" jsonschema:"Optional replacement display name"`
	CustomInstructions      *string               `json:"custom_instructions,omitempty" jsonschema:"Optional replacement instructions. Send an empty string to clear them"`
	ChannelAccessLevel      *agentAccessLevel     `json:"channel_access_level,omitempty" jsonschema:"Optional replacement channel access level: all, allow, block, or none,enum=all,enum=allow,enum=block,enum=none"`
	ChannelIDs              *[]string             `json:"channel_ids,omitempty" jsonschema:"Optional replacement channel IDs. Send [] to clear"`
	UserAccessLevel         *agentAccessLevel     `json:"user_access_level,omitempty" jsonschema:"Optional replacement user access level: all, allow, block, or none,enum=all,enum=allow,enum=block,enum=none"`
	UserIDs                 *[]string             `json:"user_ids,omitempty" jsonschema:"Optional replacement user IDs. Send [] to clear"`
	TeamIDs                 *[]string             `json:"team_ids,omitempty" jsonschema:"Optional replacement team IDs. Send [] to clear"`
	AdminUserIDs            *[]string             `json:"admin_user_ids,omitempty" jsonschema:"Optional replacement admin user IDs. Send [] to clear"`
	EnabledMCPToolsMode     *agentToolPolicyMode  `json:"enabled_mcp_tools_mode,omitempty" jsonschema:"Optional replacement MCP tool policy: allow_all, allow_none, or allow_list,enum=allow_all,enum=allow_none,enum=allow_list"`
	EnabledMCPTools         *[]llm.EnabledMCPTool `json:"enabled_mcp_tools,omitempty" jsonschema:"Optional replacement MCP tool allowlist. Send [] with enabled_mcp_tools_mode=allow_list to set an empty allowlist"`
	Model                   *string               `json:"model,omitempty" jsonschema:"Optional replacement model. Send an empty string to clear it"`
	EnableVision            *bool                 `json:"enable_vision,omitempty" jsonschema:"Optional replacement value"`
	DisableTools            *bool                 `json:"disable_tools,omitempty" jsonschema:"Optional replacement value"`
	EnabledNativeTools      *[]string             `json:"enabled_native_tools,omitempty" jsonschema:"Optional replacement native tool list. Send [] to clear"`
	ReasoningEnabled        *bool                 `json:"reasoning_enabled,omitempty" jsonschema:"Optional replacement value"`
	ReasoningEffort         *string               `json:"reasoning_effort,omitempty" jsonschema:"Optional replacement reasoning effort. Send an empty string to clear it"`
	ThinkingBudget          *int                  `json:"thinking_budget,omitempty" jsonschema:"Optional replacement thinking budget"`
	StructuredOutputEnabled *bool                 `json:"structured_output_enabled,omitempty" jsonschema:"Optional replacement value"`
}

type GetAgentsToolArgs struct {
	AgentID       string `json:"agent_id,omitempty" jsonschema:"Optional agent ID to fetch"`
	AgentUsername string `json:"agent_username,omitempty" jsonschema:"Optional agent username to fetch when the agent ID is unknown"`
}

type CreateCustomPromptToolArgs struct {
	Name        string `json:"name" jsonschema:"The custom prompt name,minLength=1"`
	Description string `json:"description,omitempty" jsonschema:"Optional prompt description"`
	Template    string `json:"template" jsonschema:"The prompt template text,minLength=1"`
	IsShared    *bool  `json:"is_shared,omitempty" jsonschema:"Optional; whether the prompt is shared with other users. Defaults to false"`
}

type GetCustomPromptToolArgs struct {
	PromptID   string `json:"prompt_id,omitempty" jsonschema:"Optional prompt ID to fetch"`
	PromptName string `json:"prompt_name,omitempty" jsonschema:"Optional exact prompt name to fetch when the prompt ID is unknown"`
}

type UpdateCustomPromptToolArgs struct {
	PromptID    string  `json:"prompt_id,omitempty" jsonschema:"Optional prompt ID to update"`
	PromptName  string  `json:"prompt_name,omitempty" jsonschema:"Optional exact prompt name to update when the prompt ID is unknown"`
	Name        *string `json:"name,omitempty" jsonschema:"Optional replacement prompt name"`
	Description *string `json:"description,omitempty" jsonschema:"Optional replacement prompt description. Send an empty string to clear it"`
	Template    *string `json:"template,omitempty" jsonschema:"Optional replacement template. Send an empty string to clear it"`
	IsShared    *bool   `json:"is_shared,omitempty" jsonschema:"Optional replacement sharing flag"`
}

func (p *MattermostToolProvider) getAgentManagementTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "get_agents",
			Description: "Get visible self-serve AI agents from the Mattermost Agents plugin. With no arguments, returns every visible agent including IDs. Optionally filter by agent_id or agent_username when you want a specific agent before updating it.",
			Schema:      NewJSONSchemaForAccessMode[GetAgentsToolArgs](string(p.accessMode)),
			Resolver:    p.toolGetAgents,
		},
		{
			Name:        "create_agent",
			Description: "Create a new self-serve AI agent in the Mattermost Agents plugin. Provide display_name, username, and either service_id or service_name. Defaults mirror the UI: tools enabled, vision enabled, reasoning enabled, web_search native tool enabled, broad channel/user access, and all MCP tools allowed unless you narrow them.",
			Schema:      NewJSONSchemaForAccessMode[CreateAgentToolArgs](string(p.accessMode)),
			Resolver:    p.toolCreateAgent,
		},
		{
			Name:        "update_agent",
			Description: "Update an existing self-serve AI agent in the Mattermost Agents plugin. Identify the agent with agent_id or agent_username. This tool fetches the current agent, merges only the fields you supply, and saves the full updated configuration for you.",
			Schema:      NewJSONSchemaForAccessMode[UpdateAgentToolArgs](string(p.accessMode)),
			Resolver:    p.toolUpdateAgent,
		},
		{
			Name:        "get_custom_prompts",
			Description: "Get visible custom prompt templates from the Mattermost Agents plugin. With no arguments, returns every visible prompt including IDs. Optionally filter by prompt_id or prompt_name when you want a specific prompt before updating it.",
			Schema:      NewJSONSchemaForAccessMode[GetCustomPromptToolArgs](string(p.accessMode)),
			Resolver:    p.toolGetCustomPrompts,
		},
		{
			Name:        "create_custom_prompt",
			Description: "Create a new custom prompt template in the Mattermost Agents plugin. Provide name and template. Optionally provide description and is_shared.",
			Schema:      NewJSONSchemaForAccessMode[CreateCustomPromptToolArgs](string(p.accessMode)),
			Resolver:    p.toolCreateCustomPrompt,
		},
		{
			Name:        "update_custom_prompt",
			Description: "Update an existing custom prompt template in the Mattermost Agents plugin. Identify it with prompt_id or prompt_name. This tool fetches the current prompt, merges only the fields you supply, and saves the result.",
			Schema:      NewJSONSchemaForAccessMode[UpdateCustomPromptToolArgs](string(p.accessMode)),
			Resolver:    p.toolUpdateCustomPrompt,
		},
	}
}

func (p *MattermostToolProvider) toolGetAgents(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetAgentsToolArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_agents: %w", err)
	}

	agents, err := doPluginJSON[[]llm.BotConfig](mcpContext, http.MethodGet, "/agents", nil)
	if err != nil {
		return "failed to list agents", fmt.Errorf("get_agents request failed: %w", err)
	}

	filteredAgents, err := filterAgentsByLookup(agents, args.AgentID, args.AgentUsername)
	if err != nil {
		return "failed to resolve agent", err
	}

	return marshalToolResult(filteredAgents)
}

func (p *MattermostToolProvider) toolCreateAgent(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreateAgentToolArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_agent: %w", err)
	}

	if strings.TrimSpace(args.DisplayName) == "" {
		return "display_name is required", fmt.Errorf("display_name cannot be empty")
	}
	if strings.TrimSpace(args.Username) == "" {
		return "username is required", fmt.Errorf("username cannot be empty")
	}

	serviceID, err := resolveServiceID(mcpContext, args.ServiceID, args.ServiceName)
	if err != nil {
		return "failed to resolve service", err
	}

	enabledMCPTools, err := resolveEnabledMCPTools(args.EnabledMCPToolsMode, args.EnabledMCPTools, nil, true)
	if err != nil {
		return "invalid enabled_mcp_tools configuration", err
	}

	body := agentAPIRequest{
		DisplayName:             args.DisplayName,
		Username:                strings.TrimPrefix(strings.TrimSpace(args.Username), "@"),
		ServiceID:               serviceID,
		CustomInstructions:      args.CustomInstructions,
		ChannelAccessLevel:      int(channelAccessLevelOrDefault(args.ChannelAccessLevel, llm.ChannelAccessLevelAll)),
		ChannelIDs:              args.ChannelIDs,
		UserAccessLevel:         int(userAccessLevelOrDefault(args.UserAccessLevel, llm.UserAccessLevelAll)),
		UserIDs:                 args.UserIDs,
		TeamIDs:                 args.TeamIDs,
		AdminUserIDs:            args.AdminUserIDs,
		EnabledMCPTools:         enabledMCPTools,
		Model:                   args.Model,
		EnableVision:            boolOrDefault(args.EnableVision, true),
		DisableTools:            boolOrDefault(args.DisableTools, false),
		EnabledNativeTools:      stringSliceOrDefault(args.EnabledNativeTools, []string{"web_search"}),
		ReasoningEnabled:        boolOrDefault(args.ReasoningEnabled, true),
		ReasoningEffort:         stringOrDefault(args.ReasoningEffort, "medium"),
		ThinkingBudget:          intOrDefault(args.ThinkingBudget, 0),
		StructuredOutputEnabled: boolOrDefault(args.StructuredOutputEnabled, false),
	}

	createdAgent, err := doPluginJSON[llm.BotConfig](mcpContext, http.MethodPost, "/agents", body)
	if err != nil {
		return "failed to create agent", fmt.Errorf("create_agent request failed: %w", err)
	}

	return marshalToolResult(createdAgent)
}

func (p *MattermostToolProvider) toolUpdateAgent(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args UpdateAgentToolArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool update_agent: %w", err)
	}

	currentAgent, err := resolveAgent(mcpContext, args.AgentID, args.AgentUsername)
	if err != nil {
		return "failed to resolve agent", err
	}

	body := agentToAPIRequest(currentAgent)

	if args.DisplayName != nil {
		body.DisplayName = *args.DisplayName
	}
	if args.CustomInstructions != nil {
		body.CustomInstructions = *args.CustomInstructions
	}
	if args.ChannelAccessLevel != nil {
		level, parseErr := parseChannelAccessLevel(*args.ChannelAccessLevel)
		if parseErr != nil {
			return "invalid channel_access_level", parseErr
		}
		body.ChannelAccessLevel = int(level)
	}
	if args.ChannelIDs != nil {
		body.ChannelIDs = *args.ChannelIDs
	}
	if args.UserAccessLevel != nil {
		level, parseErr := parseUserAccessLevel(*args.UserAccessLevel)
		if parseErr != nil {
			return "invalid user_access_level", parseErr
		}
		body.UserAccessLevel = int(level)
	}
	if args.UserIDs != nil {
		body.UserIDs = *args.UserIDs
	}
	if args.TeamIDs != nil {
		body.TeamIDs = *args.TeamIDs
	}
	if args.AdminUserIDs != nil {
		body.AdminUserIDs = *args.AdminUserIDs
	}
	if args.Model != nil {
		body.Model = *args.Model
	}
	if args.EnableVision != nil {
		body.EnableVision = *args.EnableVision
	}
	if args.DisableTools != nil {
		body.DisableTools = *args.DisableTools
	}
	if args.EnabledNativeTools != nil {
		body.EnabledNativeTools = *args.EnabledNativeTools
	}
	if args.ReasoningEnabled != nil {
		body.ReasoningEnabled = *args.ReasoningEnabled
	}
	if args.ReasoningEffort != nil {
		body.ReasoningEffort = *args.ReasoningEffort
	}
	if args.ThinkingBudget != nil {
		body.ThinkingBudget = *args.ThinkingBudget
	}
	if args.StructuredOutputEnabled != nil {
		body.StructuredOutputEnabled = *args.StructuredOutputEnabled
	}

	if args.ServiceID != nil || args.ServiceName != nil {
		body.ServiceID, err = resolveServiceID(mcpContext, stringPointerValue(args.ServiceID), stringPointerValue(args.ServiceName))
		if err != nil {
			return "failed to resolve service", err
		}
	}

	enabledMCPTools, err := resolveEnabledMCPTools(pointerValue(args.EnabledMCPToolsMode), slicePointerValue(args.EnabledMCPTools), currentAgent.EnabledMCPTools, false)
	if err != nil {
		return "invalid enabled_mcp_tools configuration", err
	}
	body.EnabledMCPTools = enabledMCPTools

	updatedAgent, err := doPluginJSON[llm.BotConfig](mcpContext, http.MethodPut, "/agents/"+currentAgent.ID, body)
	if err != nil {
		return "failed to update agent", fmt.Errorf("update_agent request failed: %w", err)
	}

	return marshalToolResult(updatedAgent)
}

func (p *MattermostToolProvider) toolGetCustomPrompts(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetCustomPromptToolArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_custom_prompts: %w", err)
	}

	prompts, err := doPluginJSON[[]customprompts.CustomPrompt](mcpContext, http.MethodGet, "/custom-prompts", nil)
	if err != nil {
		return "failed to list custom prompts", fmt.Errorf("get_custom_prompts request failed: %w", err)
	}

	filteredPrompts, err := filterPromptsByLookup(prompts, args.PromptID, args.PromptName)
	if err != nil {
		return "failed to resolve custom prompt", err
	}

	return marshalToolResult(filteredPrompts)
}

func (p *MattermostToolProvider) toolCreateCustomPrompt(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreateCustomPromptToolArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_custom_prompt: %w", err)
	}

	if strings.TrimSpace(args.Name) == "" {
		return "name is required", fmt.Errorf("name cannot be empty")
	}
	if strings.TrimSpace(args.Template) == "" {
		return "template is required", fmt.Errorf("template cannot be empty")
	}

	createdPrompt, err := doPluginJSON[customprompts.CustomPrompt](mcpContext, http.MethodPost, "/custom-prompts", map[string]any{
		"name":        args.Name,
		"description": args.Description,
		"template":    args.Template,
		"is_shared":   boolOrDefault(args.IsShared, false),
	})
	if err != nil {
		return "failed to create custom prompt", fmt.Errorf("create_custom_prompt request failed: %w", err)
	}

	return marshalToolResult(createdPrompt)
}

func (p *MattermostToolProvider) toolUpdateCustomPrompt(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args UpdateCustomPromptToolArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool update_custom_prompt: %w", err)
	}

	currentPrompt, err := resolveCustomPrompt(mcpContext, args.PromptID, args.PromptName)
	if err != nil {
		return "failed to resolve custom prompt", err
	}

	body := map[string]any{
		"name":        currentPrompt.Name,
		"description": currentPrompt.Description,
		"template":    currentPrompt.Template,
		"is_shared":   currentPrompt.IsShared,
	}
	if args.Name != nil {
		body["name"] = *args.Name
	}
	if args.Description != nil {
		body["description"] = *args.Description
	}
	if args.Template != nil {
		body["template"] = *args.Template
	}
	if args.IsShared != nil {
		body["is_shared"] = *args.IsShared
	}

	if _, err := doPluginNoContent(mcpContext, http.MethodPut, "/custom-prompts/"+currentPrompt.ID, body); err != nil {
		return "failed to update custom prompt", fmt.Errorf("update_custom_prompt request failed: %w", err)
	}

	updatedPrompt, err := resolveCustomPrompt(mcpContext, currentPrompt.ID, "")
	if err != nil {
		return "custom prompt updated but could not be reloaded", err
	}

	return marshalToolResult(updatedPrompt)
}

func doPluginJSON[T any](mcpContext *MCPToolContext, method, route string, payload any) (T, error) {
	var zero T

	response, err := doPluginRequest(mcpContext, method, route, payload)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return zero, pluginRequestError(method, route, response)
	}

	result, _, err := model.DecodeJSONFromResponse[T](response)
	if err != nil {
		return zero, fmt.Errorf("failed to decode %s %s response: %w", method, route, err)
	}

	return result, nil
}

func doPluginNoContent(mcpContext *MCPToolContext, method, route string, payload any) (*http.Response, error) {
	response, err := doPluginRequest(mcpContext, method, route, payload)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, pluginRequestError(method, route, response)
	}

	return response, nil
}

func doPluginRequest(mcpContext *MCPToolContext, method, route string, payload any) (*http.Response, error) {
	if mcpContext == nil || mcpContext.Client == nil {
		return nil, fmt.Errorf("client not available in context")
	}

	pluginURL := strings.TrimRight(mcpContext.Client.URL, "/") + pluginRoutePrefix + route

	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal %s %s payload: %w", method, route, err)
		}
		body = bytes.NewReader(data)
	}

	request, err := http.NewRequestWithContext(mcpContext.Ctx, method, pluginURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to build %s %s request: %w", method, route, err)
	}

	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if mcpContext.Client.AuthToken != "" {
		request.Header.Set(model.HeaderAuth, mcpContext.Client.AuthType+" "+mcpContext.Client.AuthToken)
	}
	for key, value := range mcpContext.Client.HTTPHeader {
		request.Header.Set(key, value)
	}

	response, err := mcpContext.Client.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to call %s %s: %w", method, route, err)
	}

	return response, nil
}

func pluginRequestError(method, route string, response *http.Response) error {
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return fmt.Errorf("%s %s failed with status %d and unreadable body: %w", method, route, response.StatusCode, readErr)
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("%s %s failed with status %d", method, route, response.StatusCode)
	}

	return fmt.Errorf("%s %s failed with status %d: %s", method, route, response.StatusCode, message)
}

func resolveServiceID(mcpContext *MCPToolContext, serviceID, serviceName string) (string, error) {
	services, err := doPluginJSON[[]serviceInfoResponse](mcpContext, http.MethodGet, "/services", nil)
	if err != nil {
		return "", fmt.Errorf("failed to list services: %w", err)
	}

	trimmedID := strings.TrimSpace(serviceID)
	trimmedName := strings.TrimSpace(serviceName)
	if trimmedID == "" && trimmedName == "" {
		return "", fmt.Errorf("service_id or service_name is required")
	}

	var matchedByID *serviceInfoResponse
	var matchedByName *serviceInfoResponse
	for i := range services {
		service := services[i]
		if trimmedID != "" && service.ID == trimmedID {
			matchedByID = &service
		}
		if trimmedName != "" && strings.EqualFold(service.Name, trimmedName) {
			if matchedByName != nil && matchedByName.ID != service.ID {
				return "", fmt.Errorf("service_name %q matched multiple configured services; use service_id instead", trimmedName)
			}
			matchedByName = &service
		}
	}

	switch {
	case matchedByID != nil && matchedByName != nil && matchedByID.ID != matchedByName.ID:
		return "", fmt.Errorf("service_id %q and service_name %q refer to different configured services", trimmedID, trimmedName)
	case matchedByID != nil:
		return matchedByID.ID, nil
	case matchedByName != nil:
		return matchedByName.ID, nil
	case trimmedID != "":
		return "", fmt.Errorf("service_id %q was not found", trimmedID)
	default:
		return "", fmt.Errorf("service_name %q was not found", trimmedName)
	}
}

func resolveAgent(mcpContext *MCPToolContext, agentID, agentUsername string) (llm.BotConfig, error) {
	agents, err := doPluginJSON[[]llm.BotConfig](mcpContext, http.MethodGet, "/agents", nil)
	if err != nil {
		return llm.BotConfig{}, fmt.Errorf("failed to list agents: %w", err)
	}

	filteredAgents, err := filterAgentsByLookup(agents, agentID, agentUsername)
	if err != nil {
		return llm.BotConfig{}, err
	}
	if len(filteredAgents) == 0 {
		return llm.BotConfig{}, fmt.Errorf("agent lookup returned no results")
	}

	return filteredAgents[0], nil
}

func filterAgentsByLookup(agents []llm.BotConfig, agentID, agentUsername string) ([]llm.BotConfig, error) {
	trimmedID := strings.TrimSpace(agentID)
	trimmedUsername := strings.TrimPrefix(strings.TrimSpace(agentUsername), "@")
	if trimmedID == "" && trimmedUsername == "" {
		return agents, nil
	}

	var matchedByID *llm.BotConfig
	var matchedByUsername *llm.BotConfig
	for i := range agents {
		agent := agents[i]
		if trimmedID != "" && agent.ID == trimmedID {
			matchedByID = &agent
		}
		if trimmedUsername != "" && strings.EqualFold(agent.Name, trimmedUsername) {
			if matchedByUsername != nil && matchedByUsername.ID != agent.ID {
				return nil, fmt.Errorf("agent_username %q matched multiple agents; use agent_id instead", trimmedUsername)
			}
			matchedByUsername = &agent
		}
	}

	switch {
	case matchedByID != nil && matchedByUsername != nil && matchedByID.ID != matchedByUsername.ID:
		return nil, fmt.Errorf("agent_id %q and agent_username %q refer to different agents", trimmedID, trimmedUsername)
	case matchedByID != nil:
		return []llm.BotConfig{*matchedByID}, nil
	case matchedByUsername != nil:
		return []llm.BotConfig{*matchedByUsername}, nil
	case trimmedID != "":
		return nil, fmt.Errorf("agent_id %q was not found", trimmedID)
	default:
		return nil, fmt.Errorf("agent_username %q was not found", trimmedUsername)
	}
}

func resolveCustomPrompt(mcpContext *MCPToolContext, promptID, promptName string) (customprompts.CustomPrompt, error) {
	prompts, err := doPluginJSON[[]customprompts.CustomPrompt](mcpContext, http.MethodGet, "/custom-prompts", nil)
	if err != nil {
		return customprompts.CustomPrompt{}, fmt.Errorf("failed to list custom prompts: %w", err)
	}

	filteredPrompts, err := filterPromptsByLookup(prompts, promptID, promptName)
	if err != nil {
		return customprompts.CustomPrompt{}, err
	}
	if len(filteredPrompts) == 0 {
		return customprompts.CustomPrompt{}, fmt.Errorf("custom prompt lookup returned no results")
	}

	return filteredPrompts[0], nil
}

func filterPromptsByLookup(prompts []customprompts.CustomPrompt, promptID, promptName string) ([]customprompts.CustomPrompt, error) {
	trimmedID := strings.TrimSpace(promptID)
	trimmedName := strings.TrimSpace(promptName)
	if trimmedID == "" && trimmedName == "" {
		return prompts, nil
	}

	var matchedByID *customprompts.CustomPrompt
	var matchedByName *customprompts.CustomPrompt
	for i := range prompts {
		prompt := prompts[i]
		if trimmedID != "" && prompt.ID == trimmedID {
			matchedByID = &prompt
		}
		if trimmedName != "" && strings.EqualFold(prompt.Name, trimmedName) {
			if matchedByName != nil && matchedByName.ID != prompt.ID {
				return nil, fmt.Errorf("prompt_name %q matched multiple prompts; use prompt_id instead", trimmedName)
			}
			matchedByName = &prompt
		}
	}

	switch {
	case matchedByID != nil && matchedByName != nil && matchedByID.ID != matchedByName.ID:
		return nil, fmt.Errorf("prompt_id %q and prompt_name %q refer to different prompts", trimmedID, trimmedName)
	case matchedByID != nil:
		return []customprompts.CustomPrompt{*matchedByID}, nil
	case matchedByName != nil:
		return []customprompts.CustomPrompt{*matchedByName}, nil
	case trimmedID != "":
		return nil, fmt.Errorf("prompt_id %q was not found", trimmedID)
	default:
		return nil, fmt.Errorf("prompt_name %q was not found", trimmedName)
	}
}

func agentToAPIRequest(agent llm.BotConfig) agentAPIRequest {
	return agentAPIRequest{
		DisplayName:             agent.DisplayName,
		Username:                agent.Name,
		ServiceID:               agent.ServiceID,
		CustomInstructions:      agent.CustomInstructions,
		ChannelAccessLevel:      int(agent.ChannelAccessLevel),
		ChannelIDs:              agent.ChannelIDs,
		UserAccessLevel:         int(agent.UserAccessLevel),
		UserIDs:                 agent.UserIDs,
		TeamIDs:                 agent.TeamIDs,
		AdminUserIDs:            agent.AdminUserIDs,
		EnabledMCPTools:         agent.EnabledMCPTools,
		Model:                   agent.Model,
		EnableVision:            agent.EnableVision,
		DisableTools:            agent.DisableTools,
		EnabledNativeTools:      agent.EnabledNativeTools,
		ReasoningEnabled:        agent.ReasoningEnabled,
		ReasoningEffort:         agent.ReasoningEffort,
		ThinkingBudget:          agent.ThinkingBudget,
		StructuredOutputEnabled: agent.StructuredOutputEnabled,
	}
}

func resolveEnabledMCPTools(mode agentToolPolicyMode, tools, current []llm.EnabledMCPTool, useCreateDefaults bool) ([]llm.EnabledMCPTool, error) {
	if mode == "" {
		if tools != nil {
			return tools, nil
		}
		if useCreateDefaults {
			return nil, nil
		}
		return current, nil
	}

	switch mode {
	case agentToolPolicyAllowAll:
		return nil, nil
	case agentToolPolicyAllowNone:
		return []llm.EnabledMCPTool{}, nil
	case agentToolPolicyAllowList:
		if tools == nil {
			return nil, fmt.Errorf("enabled_mcp_tools_mode=allow_list requires enabled_mcp_tools")
		}
		return tools, nil
	default:
		return nil, fmt.Errorf("unsupported enabled_mcp_tools_mode %q", mode)
	}
}

func parseChannelAccessLevel(level agentAccessLevel) (llm.ChannelAccessLevel, error) {
	switch level {
	case agentAccessLevelAll:
		return llm.ChannelAccessLevelAll, nil
	case agentAccessLevelAllow:
		return llm.ChannelAccessLevelAllow, nil
	case agentAccessLevelBlock:
		return llm.ChannelAccessLevelBlock, nil
	case agentAccessLevelNone:
		return llm.ChannelAccessLevelNone, nil
	default:
		return 0, fmt.Errorf("unsupported channel_access_level %q", level)
	}
}

func parseUserAccessLevel(level agentAccessLevel) (llm.UserAccessLevel, error) {
	switch level {
	case agentAccessLevelAll:
		return llm.UserAccessLevelAll, nil
	case agentAccessLevelAllow:
		return llm.UserAccessLevelAllow, nil
	case agentAccessLevelBlock:
		return llm.UserAccessLevelBlock, nil
	case agentAccessLevelNone:
		return llm.UserAccessLevelNone, nil
	default:
		return 0, fmt.Errorf("unsupported user_access_level %q", level)
	}
}

func channelAccessLevelOrDefault(level agentAccessLevel, fallback llm.ChannelAccessLevel) llm.ChannelAccessLevel {
	if level == "" {
		return fallback
	}
	parsed, err := parseChannelAccessLevel(level)
	if err != nil {
		return fallback
	}
	return parsed
}

func userAccessLevelOrDefault(level agentAccessLevel, fallback llm.UserAccessLevel) llm.UserAccessLevel {
	if level == "" {
		return fallback
	}
	parsed, err := parseUserAccessLevel(level)
	if err != nil {
		return fallback
	}
	return parsed
}

func marshalToolResult(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool result: %w", err)
	}
	return string(data), nil
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func intOrDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func stringOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func stringSliceOrDefault(value, fallback []string) []string {
	if value == nil {
		return fallback
	}
	return value
}

func pointerValue[T ~string](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func slicePointerValue[T any](value *[]T) []T {
	if value == nil {
		return nil
	}
	return *value
}
