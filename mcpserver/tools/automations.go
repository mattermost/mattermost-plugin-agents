// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

const automationPluginAPIPath = "/plugins/com.mattermost.channel-automation/api/v1"

// isAutomationPluginInstalled probes the channel automation plugin API to check if
// the plugin is installed and reachable. Returns true if the plugin responds (even
// with an auth error), false if it 404s or is unreachable.
func (p *MattermostToolProvider) isAutomationPluginInstalled() bool {
	resp, err := http.Get(p.automationAPIURL("/flows")) //nolint:gosec
	if err != nil {
		return false
	}
	resp.Body.Close()

	// A 404 from the Mattermost server means the plugin route doesn't exist.
	// Any other status (200, 401, 403, etc.) means the plugin is installed.
	return resp.StatusCode != http.StatusNotFound
}

// AutomationAction mirrors the channel-automation plugin's Action model.
type AutomationAction struct {
	ID            string         `json:"id,omitempty"`
	Name          string         `json:"name"`
	Type          string         `json:"type"`
	ChannelID     string         `json:"channel_id,omitempty"`
	ReplyToPostID string         `json:"reply_to_post_id,omitempty"`
	Body          string         `json:"body,omitempty"`
	Config        map[string]any `json:"config,omitempty"`
}

// AutomationTrigger mirrors the channel-automation plugin's Trigger model.
type AutomationTrigger struct {
	Type      string `json:"type"`
	ChannelID string `json:"channel_id"`
}

// AutomationFlow mirrors the channel-automation plugin's Flow model.
type AutomationFlow struct {
	ID        string             `json:"id,omitempty"`
	Name      string             `json:"name"`
	Enabled   bool               `json:"enabled"`
	Trigger   AutomationTrigger  `json:"trigger"`
	Actions   []AutomationAction `json:"actions"`
	CreatedAt int64              `json:"created_at,omitempty"`
	UpdatedAt int64              `json:"updated_at,omitempty"`
	CreatedBy string             `json:"created_by,omitempty"`
}

// automationAPIURL builds a full URL for the channel automation plugin API.
func (p *MattermostToolProvider) automationAPIURL(path string) string {
	return p.mmInternalServerURL + automationPluginAPIPath + path
}

// --- Arg structs ---

// ListAutomationsArgs represents arguments for the list_automations tool.
type ListAutomationsArgs struct {
	AutomationID string `json:"automation_id,omitempty" jsonschema:"The ID of a specific automation to retrieve"`
	ChannelID    string `json:"channel_id,omitempty" jsonschema:"Filter automations by trigger channel ID"`
	Query        string `json:"query,omitempty" jsonschema:"Search automations by name (case-insensitive substring match)"`
	Enabled      *bool  `json:"enabled,omitempty" jsonschema:"Filter automations by enabled status"`
}

// CreateAutomationArgs represents arguments for the create_automation tool.
type CreateAutomationArgs struct {
	Name             string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled          bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	TriggerType      string             `json:"trigger_type" jsonschema:"The trigger type (e.g. 'new_message'),minLength=1"`
	TriggerChannelID string             `json:"trigger_channel_id" jsonschema:"The channel ID that triggers this automation,minLength=26,maxLength=26"`
	Actions          []AutomationAction `json:"actions" jsonschema:"List of actions to perform when triggered"`
}

// UpdateAutomationArgs represents arguments for the update_automation tool.
type UpdateAutomationArgs struct {
	AutomationID     string             `json:"automation_id" jsonschema:"The ID of the automation to update,minLength=1"`
	Name             string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled          bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	TriggerType      string             `json:"trigger_type" jsonschema:"The trigger type (e.g. 'new_message'),minLength=1"`
	TriggerChannelID string             `json:"trigger_channel_id" jsonschema:"The channel ID that triggers this automation,minLength=26,maxLength=26"`
	Actions          []AutomationAction `json:"actions" jsonschema:"List of actions to perform when triggered"`
}

// DeleteAutomationArgs represents arguments for the delete_automation tool.
type DeleteAutomationArgs struct {
	AutomationID string `json:"automation_id" jsonschema:"The ID of the automation to delete,minLength=1"`
}

// automationToolNames lists all automation tool names for filtering.
var automationToolNames = map[string]bool{
	"list_automations":  true,
	"create_automation": true,
	"update_automation": true,
	"delete_automation": true,
}

// IsAutomationTool returns true if the given tool name is an automation tool.
func IsAutomationTool(name string) bool {
	return automationToolNames[name]
}

// getAutomationTools returns all automation-related tools.
func (p *MattermostToolProvider) getAutomationTools() []MCPTool {
	return []MCPTool{
		{
			Name: "list_automations",
			Description: `List, search, or get channel automations (trigger-action workflows).
Provide automation_id to get a specific automation, or use optional filters: channel_id (filter by trigger channel), query (case-insensitive name search), enabled (true/false).
Returns automation details including trigger configuration and action pipeline.`,
			Schema:   llm.NewJSONSchemaFromStruct[ListAutomationsArgs](),
			Resolver: p.toolListAutomations,
		},
		{
			Name: "create_automation",
			Description: `Create a channel automation — a trigger-action workflow that fires when events occur in a channel.

TRIGGERS: Set trigger_type and trigger_channel_id.
- "message_posted": fires when any message is posted in the trigger channel.

ACTIONS: Ordered array executed sequentially. Each action has a unique "id", "name", and "type".
Action types:
1. "send_message": Posts a message as the bot.
   - channel_id: target channel ID (can differ from trigger channel)
   - body: message content (Go text/template, see below)
   - reply_to_post_id (optional): post ID to reply to, creating a thread
2. "ai_prompt": Sends a prompt to an AI agent/service via the AI plugin and stores the response. Does NOT post a message — chain a send_message action after to post it.
   - config.prompt: the prompt text (Go text/template)
   - config.provider_type: "agent" (a bot) or "service" (a raw LLM service)
   - config.provider_id: the agent's Mattermost user ID (26-char ID). Call list_agents to discover available agents and their IDs.

TEMPLATE SYNTAX: body, channel_id, reply_to_post_id, and config.prompt support Go text/template with this context:
- {{.Trigger.Post.Message}}, {{.Trigger.Post.Id}}, {{.Trigger.Post.ChannelId}}
- {{.Trigger.Channel.Id}}, {{.Trigger.Channel.Name}}, {{.Trigger.Channel.DisplayName}}
- {{.Trigger.User.Id}}, {{.Trigger.User.Username}}, {{.Trigger.User.FirstName}}, {{.Trigger.User.LastName}}
- {{(index .Steps "prev-action-id").Message}}, {{(index .Steps "prev-action-id").PostID}} — output from a previous action

EXAMPLE: AI-powered triage — when a message is posted in #support, summarize it with AI, then post the summary in #triage:
{"name":"Support Triage","enabled":true,"trigger_type":"message_posted","trigger_channel_id":"<support-ch-id>",
 "actions":[
   {"id":"summarize","name":"Summarize","type":"ai_prompt","config":{"prompt":"Summarize: {{.Trigger.Post.Message}}","provider_type":"agent","provider_id":"otto"}},
   {"id":"post","name":"Post Summary","type":"send_message","channel_id":"<triage-ch-id>","body":"From @{{.Trigger.User.Username}}:\n{{(index .Steps \"summarize\").Message}}"}
 ]}`,
			Schema:   llm.NewJSONSchemaFromStruct[CreateAutomationArgs](),
			Resolver: p.toolCreateAutomation,
		},
		{
			Name: "update_automation",
			Description: `Update an existing channel automation. Replaces the full automation definition — provide all fields, not just changed ones. Same trigger types, action types, and template syntax as create_automation.
Use list_automations first to get the current definition, then modify and pass the full updated flow.`,
			Schema:   llm.NewJSONSchemaFromStruct[UpdateAutomationArgs](),
			Resolver: p.toolUpdateAutomation,
		},
		{
			Name:        "delete_automation",
			Description: "Delete a channel automation by ID. This is permanent and cannot be undone.",
			Schema:      llm.NewJSONSchemaFromStruct[DeleteAutomationArgs](),
			Resolver:    p.toolDeleteAutomation,
		},
	}
}

// --- Resolvers ---

func (p *MattermostToolProvider) toolListAutomations(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args ListAutomationsArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool list_automations: %w", err)
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	// If a specific automation ID was requested, fetch just that one.
	if args.AutomationID != "" {
		return p.getAutomationByID(ctx, mcpContext, args.AutomationID)
	}

	// Otherwise list all and filter client-side.
	resp, err := mcpContext.Client.DoAPIRequestWithHeaders(ctx, http.MethodGet, p.automationAPIURL("/flows"), "", nil)
	if err != nil {
		return handleAutomationHTTPError(resp, err, "")
	}
	defer resp.Body.Close()

	var flows []AutomationFlow
	if err := json.NewDecoder(resp.Body).Decode(&flows); err != nil {
		return "failed to parse automation list", fmt.Errorf("failed to decode automations response: %w", err)
	}

	flows = filterAutomationFlows(flows, args.ChannelID, args.Query, args.Enabled)

	if len(flows) == 0 {
		return "No automations found matching the specified criteria.", nil
	}

	return formatAutomationFlows(flows), nil
}

func (p *MattermostToolProvider) getAutomationByID(ctx context.Context, mcpContext *MCPToolContext, id string) (string, error) {
	resp, err := mcpContext.Client.DoAPIRequestWithHeaders(ctx, http.MethodGet, p.automationAPIURL("/flows/"+id), "", nil)
	if err != nil {
		return handleAutomationHTTPError(resp, err, id)
	}
	defer resp.Body.Close()

	var flow AutomationFlow
	if err := json.NewDecoder(resp.Body).Decode(&flow); err != nil {
		return "failed to parse automation", fmt.Errorf("failed to decode automation response: %w", err)
	}

	return formatAutomationFlow(flow), nil
}

func (p *MattermostToolProvider) toolCreateAutomation(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreateAutomationArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_automation: %w", err)
	}

	if args.Name == "" {
		return "name is required", fmt.Errorf("name cannot be empty")
	}
	if args.TriggerType == "" {
		return "trigger_type is required", fmt.Errorf("trigger_type cannot be empty")
	}
	if args.TriggerChannelID == "" {
		return "trigger_channel_id is required", fmt.Errorf("trigger_channel_id cannot be empty")
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	flow := AutomationFlow{
		Name:    args.Name,
		Enabled: args.Enabled,
		Trigger: AutomationTrigger{
			Type:      args.TriggerType,
			ChannelID: args.TriggerChannelID,
		},
		Actions: args.Actions,
	}

	body, err := json.Marshal(flow)
	if err != nil {
		return "failed to encode automation", fmt.Errorf("failed to marshal automation: %w", err)
	}

	resp, err := mcpContext.Client.DoAPIRequestWithHeaders(ctx, http.MethodPost, p.automationAPIURL("/flows"), string(body), nil)
	if err != nil {
		return handleAutomationHTTPError(resp, err, "")
	}
	defer resp.Body.Close()

	var created AutomationFlow
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "failed to parse created automation", fmt.Errorf("failed to decode create response: %w", err)
	}

	return fmt.Sprintf("Successfully created automation '%s' (ID: %s).\n\n%s", created.Name, created.ID, formatAutomationFlow(created)), nil
}

func (p *MattermostToolProvider) toolUpdateAutomation(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args UpdateAutomationArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool update_automation: %w", err)
	}

	if args.AutomationID == "" {
		return "automation_id is required", fmt.Errorf("automation_id cannot be empty")
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	flow := AutomationFlow{
		ID:      args.AutomationID,
		Name:    args.Name,
		Enabled: args.Enabled,
		Trigger: AutomationTrigger{
			Type:      args.TriggerType,
			ChannelID: args.TriggerChannelID,
		},
		Actions: args.Actions,
	}

	body, err := json.Marshal(flow)
	if err != nil {
		return "failed to encode automation", fmt.Errorf("failed to marshal automation: %w", err)
	}

	resp, err := mcpContext.Client.DoAPIRequestWithHeaders(ctx, http.MethodPut, p.automationAPIURL("/flows/"+args.AutomationID), string(body), nil)
	if err != nil {
		return handleAutomationHTTPError(resp, err, args.AutomationID)
	}
	defer resp.Body.Close()

	var updated AutomationFlow
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		return "failed to parse updated automation", fmt.Errorf("failed to decode update response: %w", err)
	}

	return fmt.Sprintf("Successfully updated automation '%s' (ID: %s).\n\n%s", updated.Name, updated.ID, formatAutomationFlow(updated)), nil
}

func (p *MattermostToolProvider) toolDeleteAutomation(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args DeleteAutomationArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool delete_automation: %w", err)
	}

	if args.AutomationID == "" {
		return "automation_id is required", fmt.Errorf("automation_id cannot be empty")
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	resp, err := mcpContext.Client.DoAPIRequestWithHeaders(ctx, http.MethodDelete, p.automationAPIURL("/flows/"+args.AutomationID), "", nil)
	if err != nil {
		return handleAutomationHTTPError(resp, err, args.AutomationID)
	}
	defer resp.Body.Close()

	return fmt.Sprintf("Successfully deleted automation with ID '%s'.", args.AutomationID), nil
}

// --- Helpers ---

// handleAutomationHTTPError returns a user-friendly error message for automation API failures.
func handleAutomationHTTPError(resp *http.Response, err error, automationID string) (string, error) {
	if resp == nil {
		return "Channel Automation plugin is not installed or not reachable.", fmt.Errorf("automation plugin request failed: %w", err)
	}

	// Read the body for potential error details before checking status.
	var body []byte
	if resp.Body != nil {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "You don't have permission to manage automations. This requires SystemAdmin permission.", fmt.Errorf("automation API returned %d: %s", resp.StatusCode, string(body))
	case http.StatusNotFound:
		if automationID != "" {
			return fmt.Sprintf("Automation not found with ID '%s'.", automationID), fmt.Errorf("automation API returned 404 for ID %s", automationID)
		}
		return "Channel Automation plugin is not installed or not reachable.", fmt.Errorf("automation API returned 404: %s", string(body))
	default:
		return "Channel Automation plugin is not installed or not reachable.", fmt.Errorf("automation API returned %d: %s", resp.StatusCode, string(body))
	}
}

// filterAutomationFlows applies client-side filters to a list of flows.
func filterAutomationFlows(flows []AutomationFlow, channelID, query string, enabled *bool) []AutomationFlow {
	if channelID == "" && query == "" && enabled == nil {
		return flows
	}

	queryLower := strings.ToLower(query)
	filtered := make([]AutomationFlow, 0, len(flows))

	for _, f := range flows {
		if channelID != "" && f.Trigger.ChannelID != channelID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(f.Name), queryLower) {
			continue
		}
		if enabled != nil && f.Enabled != *enabled {
			continue
		}
		filtered = append(filtered, f)
	}

	return filtered
}

// formatAutomationFlows formats multiple automation flows for display.
func formatAutomationFlows(flows []AutomationFlow) string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d automation(s):\n\n", len(flows)))

	for i, f := range flows {
		result.WriteString(fmt.Sprintf("%d. %s\n", i+1, formatAutomationFlow(f)))
	}

	return result.String()
}

// formatAutomationFlow formats a single automation flow for display.
func formatAutomationFlow(f AutomationFlow) string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Name: %s\n", f.Name))
	result.WriteString(fmt.Sprintf("ID: %s\n", f.ID))
	result.WriteString(fmt.Sprintf("Enabled: %t\n", f.Enabled))
	result.WriteString(fmt.Sprintf("Trigger: type=%s, channel=%s\n", f.Trigger.Type, f.Trigger.ChannelID))

	if len(f.Actions) > 0 {
		result.WriteString("Actions:\n")
		for j, a := range f.Actions {
			result.WriteString(fmt.Sprintf("  %d. %s (type=%s", j+1, a.Name, a.Type))
			if a.ChannelID != "" {
				result.WriteString(fmt.Sprintf(", channel=%s", a.ChannelID))
			}
			if a.Body != "" {
				result.WriteString(fmt.Sprintf(", body=%s", a.Body))
			}
			result.WriteString(")\n")
		}
	}

	return result.String()
}
