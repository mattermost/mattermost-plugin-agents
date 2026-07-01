// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

const automationPluginAPIPath = "/plugins/com.mattermost.channel-automation/api/v1"

// isAutomationPluginInstalled probes the channel automation plugin API to check if
// the plugin is installed and reachable. Returns true if the plugin responds (even
// with an auth error), false if it 404s or is unreachable.
func (p *MattermostToolProvider) isAutomationPluginInstalled() bool {
	reqURL := p.automationAPIURL("/automations")
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		p.logger.Warn("Automation plugin check failed: bad request", "url", reqURL, "error", err.Error())
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.logger.Warn("Automation plugin check failed: connection error", "url", reqURL, "error", err.Error())
		return false
	}
	resp.Body.Close()

	// A 404 from the Mattermost server means the plugin route doesn't exist.
	// Any other status (200, 401, 403, etc.) means the plugin is installed.
	installed := resp.StatusCode != http.StatusNotFound
	return installed
}

// --- Trigger types (union: exactly one pointer field should be non-nil) ---

// AutomationTrigger defines when an automation fires. Exactly one config pointer should be set.
type AutomationTrigger struct {
	MessagePosted     *MessagePostedConfig     `json:"message_posted,omitempty"`
	Schedule          *ScheduleConfig          `json:"schedule,omitempty"`
	MembershipChanged *MembershipChangedConfig `json:"membership_changed,omitempty"`
	ChannelCreated    *ChannelCreatedConfig    `json:"channel_created,omitempty"`
	UserJoinedTeam    *UserJoinedTeamConfig    `json:"user_joined_team,omitempty"`
}

// MessagePostedConfig holds trigger config for the message_posted trigger type.
type MessagePostedConfig struct {
	ChannelID            string `json:"channel_id"`
	IncludeThreadReplies bool   `json:"include_thread_replies,omitempty"`
}

// ScheduleConfig holds trigger config for the schedule trigger type.
type ScheduleConfig struct {
	ChannelID string `json:"channel_id"`
	Interval  string `json:"interval" jsonschema:"Go duration string, minimum 5m. Examples: 1h (hourly) 24h (daily) 168h (weekly)"`
	StartAt   int64  `json:"start_at,omitempty" jsonschema:"Unix timestamp in milliseconds (UTC) for the first run — must be in the future. Repeats every interval after this time."`
}

// MembershipChangedConfig holds trigger config for the membership_changed trigger type.
type MembershipChangedConfig struct {
	ChannelID string `json:"channel_id"`
	Action    string `json:"action,omitempty"`
}

// ChannelCreatedConfig holds trigger config for the channel_created trigger type.
type ChannelCreatedConfig struct {
	TeamID string `json:"team_id"`
}

// UserJoinedTeamConfig holds trigger config for the user_joined_team trigger type.
type UserJoinedTeamConfig struct {
	TeamID   string `json:"team_id"`
	UserType string `json:"user_type,omitempty"`
}

// --- Action types (union: exactly one config pointer should be non-nil) ---

// AutomationAction defines a single step in an automation. Exactly one config pointer should be set.
type AutomationAction struct {
	ID          string                   `json:"id"`
	SendMessage *SendMessageActionConfig `json:"send_message,omitempty"`
	AIPrompt    *AIPromptActionConfig    `json:"ai_prompt,omitempty"`
	SendDM      *SendDMActionConfig      `json:"send_dm,omitempty"`
}

// SendDMActionConfig holds config for the send_dm action type.
type SendDMActionConfig struct {
	UserID  string `json:"user_id"`
	Body    string `json:"body"`
	AsBotID string `json:"as_bot_id"`
}

// SendMessageActionConfig holds config for the send_message action type.
type SendMessageActionConfig struct {
	ChannelID     string `json:"channel_id"`
	ReplyToPostID string `json:"reply_to_post_id,omitempty"`
	AsBotID       string `json:"as_bot_id,omitempty"`
	Body          string `json:"body"`
}

// AIPromptActionConfig holds config for the ai_prompt action type.
type AIPromptActionConfig struct {
	SystemPrompt string                `json:"system_prompt,omitempty"`
	Prompt       string                `json:"prompt"`
	ProviderType string                `json:"provider_type"`
	ProviderID   string                `json:"provider_id"`
	AllowedTools []string              `json:"allowed_tools,omitempty"`
	Guardrails   *AutomationGuardrails `json:"guardrails,omitempty"`
	// RequestAs selects which user the AI completion request is attributed to.
	// Allowed values: "" or "triggerer" (default — the user who triggered the
	// automation, falling back to the flow creator when the trigger has no
	// associated user) or "creator" (always the flow creator).
	RequestAs string `json:"request_as,omitempty"`
}

// Automation mirrors the channel-automation plugin's Automation model.
type Automation struct {
	ID        string             `json:"id,omitempty"`
	Name      string             `json:"name"`
	Enabled   bool               `json:"enabled"`
	Trigger   AutomationTrigger  `json:"trigger"`
	Actions   []AutomationAction `json:"actions"`
	CreatedAt int64              `json:"created_at,omitempty"`
	UpdatedAt int64              `json:"updated_at,omitempty"`
	CreatedBy string             `json:"created_by,omitempty"`
}

// AutomationGuardrails mirrors channel-automation guardrails that constrain where
// an automation may operate.
type AutomationGuardrails struct {
	ChannelIDs []string `json:"channel_ids,omitempty"`
}

// automationAPIURL builds a full URL for the channel automation plugin API.
func (p *MattermostToolProvider) automationAPIURL(path string) string {
	return p.mmServerURL + automationPluginAPIPath + path
}

// doAutomationRequest makes an HTTP request to the channel automation plugin API
// using the client's auth credentials. This bypasses Client4.DoAPIRequestWithHeaders
// which prepends /api/v4, but plugin routes are served directly at /plugins/....
// Returns the response and a non-nil error for non-2xx status codes.
func doAutomationRequest(ctx context.Context, client *model.Client4, method, reqURL, data string) (*http.Response, error) {
	var body io.Reader
	if data != "" {
		body = strings.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, err
	}
	if client.AuthToken != "" {
		req.Header.Set(model.HeaderAuth, client.AuthType+" "+client.AuthToken)
	}
	if data != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return resp, fmt.Errorf("automation API request failed with status %d", resp.StatusCode)
	}
	return resp, nil
}

// --- Arg structs ---

// ListAutomationsArgs represents arguments for the list_automations tool.
type ListAutomationsArgs struct {
	AutomationID string `json:"automation_id,omitempty" jsonschema:"The ID of a specific automation to retrieve,maxLength=26"`
	ChannelID    string `json:"channel_id,omitempty" jsonschema:"Filter automations by trigger channel ID,maxLength=26"`
}

// CreateAutomationArgs represents arguments for the create_automation tool.
type CreateAutomationArgs struct {
	Name    string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	Trigger AutomationTrigger  `json:"trigger" jsonschema:"Set exactly one trigger type"`
	Actions []AutomationAction `json:"actions" jsonschema:"Ordered list of actions to perform when triggered"`
}

// UpdateAutomationArgs represents arguments for the update_automation tool.
type UpdateAutomationArgs struct {
	AutomationID string             `json:"automation_id" jsonschema:"The ID of the automation to update,minLength=26,maxLength=26"`
	Name         string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled      bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	Trigger      AutomationTrigger  `json:"trigger" jsonschema:"Set exactly one trigger type"`
	Actions      []AutomationAction `json:"actions" jsonschema:"Ordered list of actions to perform when triggered"`
}

// DeleteAutomationArgs represents arguments for the delete_automation tool.
type DeleteAutomationArgs struct {
	AutomationID string `json:"automation_id" jsonschema:"The ID of the automation to delete,minLength=26,maxLength=26"`
}

// automationInstructionsAPIResponse matches GET .../automation-instructions on the channel-automation plugin.
type automationInstructionsAPIResponse struct {
	Instructions string `json:"instructions"`
}

// createAutomationToolDescription is the MCP tool metadata description for create_automation (registered at startup).
// Optional user-facing doc URLs come from GET /automation-instructions (instructions) after get_automation_instructions runs.
const createAutomationToolDescription = `Create a channel automation — a trigger-action workflow that fires when events occur.
IMPORTANT: Before calling this tool, you MUST call get_automation_instructions to learn the
required format for triggers, actions, and allowed_tools. Then present a summary to the user
and get their confirmation before creating.`

func (p *MattermostToolProvider) fetchAutomationInstructions(ctx context.Context, client *model.Client4) (automationInstructionsAPIResponse, error) {
	var out automationInstructionsAPIResponse
	if client == nil {
		return out, fmt.Errorf("client not available")
	}
	resp, err := doAutomationRequest(ctx, client, http.MethodGet, p.automationAPIURL("/automation-instructions"), "")
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("failed to decode automation instructions: %w", err)
	}
	return out, nil
}

// getAutomationTools returns all automation-related tools.
func (p *MattermostToolProvider) getAutomationTools() []MCPTool {
	return []MCPTool{
		{
			Name: "list_automations",
			Description: `List or get channel automations (trigger-action workflows).
Provide automation_id to get a specific automation, or use optional channel_id to filter by trigger channel.
Returns the full JSON for each automation including trigger configuration and action pipeline.`,
			Schema:    NewJSONSchemaForAccessMode[ListAutomationsArgs](string(p.accessMode)),
			Resolver:  typed("list_automations", p.toolListAutomations),
			Available: p.isAutomationPluginInstalled,
		},
		{
			Name:        "get_automation_instructions",
			Description: "Returns detailed documentation for creating and updating channel automations: triggers, actions, template syntax, allowed_tools, and required user-confirmation workflow. Call this before create_automation or update_automation.",
			Schema:      nil,
			Resolver:    typed("get_automation_instructions", p.toolGetAutomationInstructions),
			Available:   p.isAutomationPluginInstalled,
		},
		{
			Name:        "create_automation",
			Description: createAutomationToolDescription,
			Schema:      NewJSONSchemaForAccessMode[CreateAutomationArgs](string(p.accessMode)),
			Resolver:    typed("create_automation", p.toolCreateAutomation),
			Available:   p.isAutomationPluginInstalled,
		},
		{
			Name: "update_automation",
			Description: `Update an existing channel automation. Replaces the full definition — any field you
omit will be cleared. Always call list_automations first to fetch the current JSON, then modify
only what needs to change and pass the full updated automation back. Call get_automation_instructions
for trigger/action format details.
IMPORTANT: Show the user what will change and get their confirmation first.`,
			Schema:    NewJSONSchemaForAccessMode[UpdateAutomationArgs](string(p.accessMode)),
			Resolver:  typed("update_automation", p.toolUpdateAutomation),
			Available: p.isAutomationPluginInstalled,
		},
		{
			Name:        "delete_automation",
			Description: "Delete a channel automation by ID. This is permanent and cannot be undone.",
			Schema:      NewJSONSchemaForAccessMode[DeleteAutomationArgs](string(p.accessMode)),
			Resolver:    typed("delete_automation", p.toolDeleteAutomation),
			Available:   p.isAutomationPluginInstalled,
		},
	}
}

// --- Resolvers ---

func (p *MattermostToolProvider) toolGetAutomationInstructions(mcpContext *MCPToolContext, _ struct{}) (string, error) {
	payload, err := p.fetchAutomationInstructions(mcpContext.Ctx, mcpContext.Client)
	if err != nil {
		return "", fmt.Errorf("failed to fetch automation instructions from Channel Automation plugin (upgrade plugin or check connectivity): %w", err)
	}
	return payload.Instructions, nil
}

func (p *MattermostToolProvider) toolListAutomations(mcpContext *MCPToolContext, args ListAutomationsArgs) (string, error) {
	ctx := mcpContext.Ctx

	// If a specific automation ID was requested, fetch just that one.
	if args.AutomationID != "" {
		if err := requireID("automation_id", args.AutomationID); err != nil {
			return "", err
		}
		return p.getAutomationByID(ctx, mcpContext, args.AutomationID)
	}

	// Use server-side channel_id filter if provided, otherwise fetch all.
	listURL := p.automationAPIURL("/automations")
	if args.ChannelID != "" {
		listURL += "?channel_id=" + url.QueryEscape(args.ChannelID)
	}

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodGet, listURL, "")
	if err != nil {
		return "", handleAutomationHTTPError(resp, err, "")
	}
	defer resp.Body.Close()

	var automations []Automation
	if err := json.NewDecoder(resp.Body).Decode(&automations); err != nil {
		return "", fmt.Errorf("failed to decode automations response: %w", err)
	}

	if len(automations) == 0 {
		return "No automations found matching the specified criteria.", nil
	}

	return formatAutomationsJSON(automations)
}

func (p *MattermostToolProvider) getAutomationByID(ctx context.Context, mcpContext *MCPToolContext, id string) (string, error) {
	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodGet, p.automationAPIURL("/automations/"+id), "")
	if err != nil {
		return "", handleAutomationHTTPError(resp, err, id)
	}
	defer resp.Body.Close()

	var automation Automation
	if err := json.NewDecoder(resp.Body).Decode(&automation); err != nil {
		return "", fmt.Errorf("failed to decode automation response: %w", err)
	}

	return formatAutomationJSON(automation)
}

func (p *MattermostToolProvider) toolCreateAutomation(mcpContext *MCPToolContext, args CreateAutomationArgs) (string, error) {
	if args.Name == "" {
		return "", fmt.Errorf("name cannot be empty")
	}

	ctx := mcpContext.Ctx

	automation := Automation{
		Name:    args.Name,
		Enabled: args.Enabled,
		Trigger: args.Trigger,
		Actions: args.Actions,
	}

	body, err := json.Marshal(automation)
	if err != nil {
		return "", fmt.Errorf("failed to marshal automation: %w", err)
	}

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodPost, p.automationAPIURL("/automations"), string(body))
	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		p.logger.Error("Automation creation failed",
			"status", statusCode,
			"error", err.Error(),
		)
		return "", handleAutomationHTTPError(resp, err, "")
	}
	defer resp.Body.Close()

	var created Automation
	if decodeErr := json.NewDecoder(resp.Body).Decode(&created); decodeErr != nil {
		return "", fmt.Errorf("failed to decode create response: %w", decodeErr)
	}

	jsonStr, err := marshalAutomationJSON(created)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Successfully created automation '%s' (ID: %s).\n\n%s", created.Name, created.ID, jsonStr), nil
}

func (p *MattermostToolProvider) toolUpdateAutomation(mcpContext *MCPToolContext, args UpdateAutomationArgs) (string, error) {
	if err := requireID("automation_id", args.AutomationID); err != nil {
		return "", err
	}

	ctx := mcpContext.Ctx

	automation := Automation{
		ID:      args.AutomationID,
		Name:    args.Name,
		Enabled: args.Enabled,
		Trigger: args.Trigger,
		Actions: args.Actions,
	}

	body, err := json.Marshal(automation)
	if err != nil {
		return "", fmt.Errorf("failed to marshal automation: %w", err)
	}

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodPut, p.automationAPIURL("/automations/"+args.AutomationID), string(body))
	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		p.logger.Error("Automation update failed",
			"status", statusCode,
			"error", err.Error(),
		)
		return "", handleAutomationHTTPError(resp, err, args.AutomationID)
	}
	defer resp.Body.Close()

	var updated Automation
	if decodeErr := json.NewDecoder(resp.Body).Decode(&updated); decodeErr != nil {
		return "", fmt.Errorf("failed to decode update response: %w", decodeErr)
	}

	jsonStr, err := marshalAutomationJSON(updated)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Successfully updated automation '%s' (ID: %s).\n\n%s", updated.Name, updated.ID, jsonStr), nil
}

func (p *MattermostToolProvider) toolDeleteAutomation(mcpContext *MCPToolContext, args DeleteAutomationArgs) (string, error) {
	if err := requireID("automation_id", args.AutomationID); err != nil {
		return "", err
	}

	ctx := mcpContext.Ctx

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodDelete, p.automationAPIURL("/automations/"+args.AutomationID), "")
	if err != nil {
		return "", handleAutomationHTTPError(resp, err, args.AutomationID)
	}
	defer resp.Body.Close()

	return fmt.Sprintf("Successfully deleted automation with ID '%s'.", args.AutomationID), nil
}

// --- Helpers ---

// triggerChannelID extracts the channel ID from any trigger variant.
func triggerChannelID(t AutomationTrigger) string {
	if t.MessagePosted != nil {
		return t.MessagePosted.ChannelID
	}
	if t.Schedule != nil {
		return t.Schedule.ChannelID
	}
	if t.MembershipChanged != nil {
		return t.MembershipChanged.ChannelID
	}
	return ""
}

// handleAutomationHTTPError returns a user-friendly error message for automation API failures.
// The Mattermost client's DoAPIRequestWithHeaders consumes the response body for non-2xx
// status codes, so resp.Body is typically empty. The original body content is available
// in the err parameter via AppErrorFromJSON.
func handleAutomationHTTPError(resp *http.Response, err error, automationID string) error {
	if resp == nil {
		return fmt.Errorf("the Channel Automation plugin is not installed or not reachable: %w", err)
	}

	// Try reading the body, but it's usually empty because the Mattermost client
	// already consumed it. Fall back to the error message which contains the original body.
	var body []byte
	if resp.Body != nil {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	detail := strings.TrimSpace(string(body))
	if detail == "" && err != nil {
		detail = automationErrorDetail(err)
	}

	switch resp.StatusCode {
	case http.StatusBadRequest:
		if detail == "" {
			detail = "invalid request"
		}
		return fmt.Errorf("bad request: %s", detail)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("you don't have permission to manage automations for this channel")
	case http.StatusNotFound:
		if automationID != "" {
			return fmt.Errorf("automation not found with ID %q", automationID)
		}
		return fmt.Errorf("the Channel Automation plugin is not installed or not reachable")
	default:
		return fmt.Errorf("automation API returned status %d: %s", resp.StatusCode, detail)
	}
}

// automationErrorDetail extracts a user-friendly message from an error returned by
// the Mattermost client. If the error is an *AppError (response was valid AppError JSON),
// it uses the Message field. Otherwise it returns the raw error string.
func automationErrorDetail(err error) string {
	var appErr *model.AppError
	if errors.As(err, &appErr) {
		if appErr.Message != "" {
			return appErr.Message
		}
		if appErr.DetailedError != "" {
			return appErr.DetailedError
		}
	}
	return err.Error()
}

// marshalAutomationJSON returns the indented JSON representation of a single
// automation. The exact JSON returned can be passed back into update_automation to
// preserve all fields (update replaces the full definition).
func marshalAutomationJSON(a Automation) (string, error) {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal automation: %w", err)
	}
	return string(b), nil
}

// formatAutomationJSON returns a single automation as JSON, with a header.
func formatAutomationJSON(a Automation) (string, error) {
	jsonStr, err := marshalAutomationJSON(a)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Automation '%s' (ID: %s):\n\n%s", a.Name, a.ID, jsonStr), nil
}

// formatAutomationsJSON returns multiple automations as JSON, one per entry.
// Each entry contains the exact JSON expected by update_automation.
func formatAutomationsJSON(automations []Automation) (string, error) {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d automation(s):\n\n", len(automations)))
	for i, a := range automations {
		jsonStr, err := marshalAutomationJSON(a)
		if err != nil {
			return "", err
		}
		result.WriteString(fmt.Sprintf("%d. %s (ID: %s)\n%s\n\n", i+1, a.Name, a.ID, jsonStr))
	}
	return result.String(), nil
}
