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
	url := p.automationAPIURL("/flows")
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		p.logger.Warn("Automation plugin check failed: connection error", "url", url, "error", err.Error())
		return false
	}
	resp.Body.Close()

	// A 404 from the Mattermost server means the plugin route doesn't exist.
	// Any other status (200, 401, 403, etc.) means the plugin is installed.
	installed := resp.StatusCode != http.StatusNotFound
	return installed
}

// --- Trigger types (union: exactly one pointer field should be non-nil) ---

// AutomationTrigger defines when a flow fires. Exactly one config pointer should be set.
type AutomationTrigger struct {
	MessagePosted     *MessagePostedConfig     `json:"message_posted,omitempty"`
	Schedule          *ScheduleConfig          `json:"schedule,omitempty"`
	MembershipChanged *MembershipChangedConfig `json:"membership_changed,omitempty"`
	ChannelCreated    *ChannelCreatedConfig    `json:"channel_created,omitempty"`
}

// MessagePostedConfig holds trigger config for the message_posted trigger type.
type MessagePostedConfig struct {
	ChannelID string `json:"channel_id"`
}

// ScheduleConfig holds trigger config for the schedule trigger type.
type ScheduleConfig struct {
	ChannelID string `json:"channel_id"`
	Interval  string `json:"interval" jsonschema:"Go duration string, minimum 5m. Examples: 1h (hourly) 24h (daily) 168h (weekly)"`
	StartAt   int64  `json:"start_at,omitempty" jsonschema:"Unix timestamp in milliseconds for the first run. Repeats every interval after this time."`
}

// MembershipChangedConfig holds trigger config for the membership_changed trigger type.
type MembershipChangedConfig struct {
	ChannelID string `json:"channel_id"`
}

// ChannelCreatedConfig holds trigger config for the channel_created trigger type.
type ChannelCreatedConfig struct{}

// --- Action types (union: exactly one config pointer should be non-nil) ---

// AutomationAction defines a single step in a flow. Exactly one config pointer should be set.
type AutomationAction struct {
	ID          string                   `json:"id"`
	SendMessage *SendMessageActionConfig `json:"send_message,omitempty"`
	AIPrompt    *AIPromptActionConfig    `json:"ai_prompt,omitempty"`
}

// SendMessageActionConfig holds config for the send_message action type.
type SendMessageActionConfig struct {
	ChannelID     string `json:"channel_id"`
	ReplyToPostID string `json:"reply_to_post_id,omitempty"`
	Body          string `json:"body"`
}

// AIPromptActionConfig holds config for the ai_prompt action type.
type AIPromptActionConfig struct {
	SystemPrompt    string          `json:"system_prompt,omitempty"`
	Prompt          string          `json:"prompt"`
	ProviderType    string          `json:"provider_type"`
	ProviderID      string          `json:"provider_id"`
	AllowedTools    []string        `json:"allowed_tools,omitempty"`
	ToolConstraints ToolConstraints `json:"tool_constraints,omitempty"`
}

// ToolConstraints maps tool names to their parameter constraints.
type ToolConstraints map[string]map[string]ParamConstraint

// ParamConstraint defines allowed values for a tool parameter.
type ParamConstraint struct {
	AllowedValues  []string        `json:"allowed_values,omitempty"`
	FromToolOutput []OutputBinding `json:"from_tool_output,omitempty"`
}

// OutputBinding declares that values from a source tool's output should be accepted.
type OutputBinding struct {
	Tool  string `json:"tool"`
	Field string `json:"field"`
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
	Name    string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	Trigger AutomationTrigger  `json:"trigger" jsonschema:"Set exactly one trigger type"`
	Actions []AutomationAction `json:"actions" jsonschema:"Ordered list of actions to perform when triggered"`
}

// UpdateAutomationArgs represents arguments for the update_automation tool.
type UpdateAutomationArgs struct {
	AutomationID string             `json:"automation_id" jsonschema:"The ID of the automation to update,minLength=1"`
	Name         string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled      bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	Trigger      AutomationTrigger  `json:"trigger" jsonschema:"Set exactly one trigger type"`
	Actions      []AutomationAction `json:"actions" jsonschema:"Ordered list of actions to perform when triggered"`
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
			Description: `Create a channel automation — a trigger-action workflow that fires when events occur.
Requires channel admin (or system admin) permission for the trigger channel.

IMPORTANT WORKFLOW — ALWAYS CONFIRM BEFORE CREATING:
Before calling this tool, you MUST present a plain-language summary to the user and get their
explicit confirmation. Even if the user provided all details, always present the full summary.

The summary must include:
1. TRIGGER: What event fires this automation and its scope.
2. AI TOOLS: Which tools the AI agent will have access to and what each one can do.
   - Without tools, the agent can only generate text from its built-in knowledge — it cannot
     read any Mattermost data or take any actions.
   - With tools, the agent inherits YOUR permissions — it can access anything you can access
     unless tool_constraints are used to limit it.
   Explain what each granted tool does so the user understands the access they are giving.
3. OUTPUT: Where the automation will post results — name the specific channel(s).
4. CONSTRAINTS: Whether tool_constraints lock specific tools to specific values (e.g.,
   search_posts locked to certain channel_ids), or whether tools have full unrestricted
   access across everything the user can see.

Format as a numbered list, then ask the user to confirm. Only call create_automation after
the user says yes.

If the user's request is missing details (trigger channel, output channel, which tools),
ask clarifying questions BEFORE presenting the summary.

TRIGGERS: Set exactly one trigger type inside the "trigger" object.
- "message_posted": fires when any message is posted in the channel. Note: fires on EVERY message in the channel, including bot messages. High-traffic channels will trigger frequently.
  {"trigger": {"message_posted": {"channel_id": "<channel-id>"}}}
- "schedule": fires on a recurring schedule.
  - interval: Go duration string (minimum "5m"). Examples: "1h" (hourly), "24h" (daily), "168h" (weekly).
  - start_at (optional): unix timestamp in milliseconds for the first run. The automation fires at this time, then repeats every interval. If omitted or in the past, the first run happens immediately. Use this to schedule a daily recap at e.g. 9am.
  {"trigger": {"schedule": {"channel_id": "<channel-id>", "interval": "24h", "start_at": 1741615200000}}}
- "membership_changed": fires when a member joins or leaves the channel.
  {"trigger": {"membership_changed": {"channel_id": "<channel-id>"}}}
- "channel_created": fires when any new public channel is created. Note: server-wide — fires for every new public channel created by any user.
  {"trigger": {"channel_created": {}}}

ACTIONS: Ordered array executed sequentially. Each action has a unique "id" and exactly one action config.
Action types:
1. "send_message": Posts a message as the bot.
   {"id": "post", "send_message": {"channel_id": "<ch>", "body": "Hello!", "reply_to_post_id": "<optional post id>"}}
2. "ai_prompt": Sends a prompt to an AI agent/service and stores the response. Does NOT post a message — chain a send_message action after to post it.
   {"id": "ask", "ai_prompt": {"prompt": "...", "provider_type": "agent", "provider_id": "<agent-user-id>", "system_prompt": "...", "allowed_tools": ["tool1"], "tool_constraints": {"tool1": {"param1": {"allowed_values": ["a","b"]}}}}}
   - provider_type: "agent" (a bot) or "service" (a raw LLM service)
   - provider_id: the agent's Mattermost user ID (26-char ID). Call list_agents to discover available agents and their IDs.
   - system_prompt (optional): system instructions for the AI
   - allowed_tools: list of tools the AI agent is allowed to call. WITHOUT this, the agent has NO tool access and can only generate text from its built-in knowledge — it cannot read any Mattermost data or take any actions. With tools, the agent inherits the creating user's permissions and can access anything they can access. IMPORTANT: Only include tools the user has explicitly agreed to. Each tool grants capabilities — e.g., search_posts can read messages across any channel the user has access to, create_post can post in any channel the user is in. Always explain what each tool does in your summary. Prefer the minimum set of tools needed.
   - tool_constraints (recommended when granting tools): lock specific tool parameters to specific values. For example, constrain search_posts to only certain channel_ids so the agent cannot search across all channels the user has access to. Tell the user they can lock tools to certain values to limit the agent's scope. Always consider adding constraints when the automation only needs access to specific channels or teams.

TEMPLATE SYNTAX: body, channel_id, reply_to_post_id, prompt, and system_prompt support Go text/template with this context:
- {{.Trigger.Post.Message}}, {{.Trigger.Post.Id}}, {{.Trigger.Post.ChannelId}}
- {{.Trigger.Channel.Id}}, {{.Trigger.Channel.Name}}, {{.Trigger.Channel.DisplayName}}
- {{.Trigger.User.Id}}, {{.Trigger.User.Username}}, {{.Trigger.User.FirstName}}, {{.Trigger.User.LastName}}
- {{(index .Steps "prev-action-id").Message}}, {{(index .Steps "prev-action-id").PostID}} — output from a previous action

EXAMPLE: AI-powered triage — when a message is posted in #support, summarize it with AI, then post the summary in #triage:
{"name":"Support Triage","enabled":true,
 "trigger":{"message_posted":{"channel_id":"<support-ch-id>"}},
 "actions":[
   {"id":"summarize","ai_prompt":{"prompt":"Summarize: {{.Trigger.Post.Message}}","provider_type":"agent","provider_id":"<agent-id>","allowed_tools":["search_posts"]}},
   {"id":"post","send_message":{"channel_id":"<triage-ch-id>","body":"From @{{.Trigger.User.Username}}:\n{{(index .Steps \"summarize\").Message}}"}}
 ]}`,
			Schema:   llm.NewJSONSchemaFromStruct[CreateAutomationArgs](),
			Resolver: p.toolCreateAutomation,
		},
		{
			Name: "update_automation",
			Description: `Update an existing channel automation. Replaces the full automation definition — provide all fields, not just changed ones. Same trigger types, action types, template syntax, and allowed_tools guidance as create_automation.
Use list_automations first to get the current definition, then modify and pass the full updated flow. Remember: ai_prompt actions need allowed_tools to be useful.

IMPORTANT: Before calling this tool, show the user what will change in plain language and
get their confirmation. Highlight any changes to trigger scope, allowed_tools, or output channels.`,
			Schema:   llm.NewJSONSchemaFromStruct[UpdateAutomationArgs](),
			Resolver: p.toolUpdateAutomation,
		},
		{
			Name: "delete_automation",
			Description: "Delete a channel automation by ID. This is permanent and cannot be undone.",
			Schema:   llm.NewJSONSchemaFromStruct[DeleteAutomationArgs](),
			Resolver: p.toolDeleteAutomation,
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
	if err := validateTrigger(args.Trigger); err != nil {
		return err.Error(), err
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	flow := AutomationFlow{
		Name:    args.Name,
		Enabled: args.Enabled,
		Trigger: args.Trigger,
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
		Trigger: args.Trigger,
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

// validateTrigger ensures exactly one trigger variant is set.
func validateTrigger(t AutomationTrigger) error {
	count := 0
	if t.MessagePosted != nil {
		count++
	}
	if t.Schedule != nil {
		count++
	}
	if t.MembershipChanged != nil {
		count++
	}
	if t.ChannelCreated != nil {
		count++
	}
	if count == 0 {
		return fmt.Errorf("trigger is required: set exactly one of message_posted, schedule, membership_changed, or channel_created")
	}
	if count > 1 {
		return fmt.Errorf("trigger must have exactly one type set, but %d were provided", count)
	}
	return nil
}

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

// triggerTypeName returns the trigger type name based on which config is present.
func triggerTypeName(t AutomationTrigger) string {
	if t.MessagePosted != nil {
		return "message_posted"
	}
	if t.Schedule != nil {
		return "schedule"
	}
	if t.MembershipChanged != nil {
		return "membership_changed"
	}
	if t.ChannelCreated != nil {
		return "channel_created"
	}
	return "unknown"
}

// actionTypeName returns the action type name based on which config is present.
func actionTypeName(a AutomationAction) string {
	if a.SendMessage != nil {
		return "send_message"
	}
	if a.AIPrompt != nil {
		return "ai_prompt"
	}
	return "unknown"
}

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
	case http.StatusBadRequest:
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = "invalid request"
		}
		return fmt.Sprintf("Bad request: %s", detail), fmt.Errorf("automation API returned 400: %s", detail)
	case http.StatusUnauthorized, http.StatusForbidden:
		return "You don't have permission to manage automations for this channel.", fmt.Errorf("automation API returned %d: %s", resp.StatusCode, string(body))
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
		if channelID != "" && triggerChannelID(f.Trigger) != channelID {
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

	typeName := triggerTypeName(f.Trigger)
	chID := triggerChannelID(f.Trigger)
	if chID != "" {
		result.WriteString(fmt.Sprintf("Trigger: type=%s, channel=%s", typeName, chID))
	} else {
		result.WriteString(fmt.Sprintf("Trigger: type=%s", typeName))
	}
	if f.Trigger.Schedule != nil && f.Trigger.Schedule.Interval != "" {
		result.WriteString(fmt.Sprintf(", interval=%s", f.Trigger.Schedule.Interval))
	}
	result.WriteString("\n")

	if len(f.Actions) > 0 {
		result.WriteString("Actions:\n")
		for j, a := range f.Actions {
			typName := actionTypeName(a)
			result.WriteString(fmt.Sprintf("  %d. id=%s (type=%s", j+1, a.ID, typName))
			if a.SendMessage != nil {
				if a.SendMessage.ChannelID != "" {
					result.WriteString(fmt.Sprintf(", channel=%s", a.SendMessage.ChannelID))
				}
				if a.SendMessage.Body != "" {
					result.WriteString(fmt.Sprintf(", body=%s", a.SendMessage.Body))
				}
			}
			if a.AIPrompt != nil {
				if a.AIPrompt.Prompt != "" {
					result.WriteString(fmt.Sprintf(", prompt=%s", a.AIPrompt.Prompt))
				}
				if a.AIPrompt.SystemPrompt != "" {
					result.WriteString(fmt.Sprintf(", system_prompt=%s", a.AIPrompt.SystemPrompt))
				}
				if len(a.AIPrompt.AllowedTools) > 0 {
					result.WriteString(fmt.Sprintf(", allowed_tools=%v", a.AIPrompt.AllowedTools))
				}
				if len(a.AIPrompt.ToolConstraints) > 0 {
					result.WriteString(", tool_constraints=<configured>")
				}
			}
			result.WriteString(")\n")
		}
	}

	return result.String()
}
