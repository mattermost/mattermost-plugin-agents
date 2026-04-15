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

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
)

const automationPluginAPIPath = "/plugins/com.mattermost.channel-automation/api/v1"

// isAutomationPluginInstalled probes the channel automation plugin API to check if
// the plugin is installed and reachable. Returns true if the plugin responds (even
// with an auth error), false if it 404s or is unreachable.
func (p *MattermostToolProvider) isAutomationPluginInstalled() bool {
	reqURL := p.automationAPIURL("/flows")
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

// AutomationTrigger defines when a flow fires. Exactly one config pointer should be set.
type AutomationTrigger struct {
	MessagePosted     *MessagePostedConfig     `json:"message_posted,omitempty"`
	Schedule          *ScheduleConfig          `json:"schedule,omitempty"`
	MembershipChanged *MembershipChangedConfig `json:"membership_changed,omitempty"`
	ChannelCreated    *ChannelCreatedConfig    `json:"channel_created,omitempty"`
	UserJoinedTeam    *UserJoinedTeamConfig    `json:"user_joined_team,omitempty"`
}

// MessagePostedConfig holds trigger config for the message_posted trigger type.
type MessagePostedConfig struct {
	ChannelID string `json:"channel_id"`
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
}

// ChannelCreatedConfig holds trigger config for the channel_created trigger type.
type ChannelCreatedConfig struct{}

// UserJoinedTeamConfig holds trigger config for the user_joined_team trigger type.
type UserJoinedTeamConfig struct {
	TeamID string `json:"team_id"`
}

// --- Action types (union: exactly one config pointer should be non-nil) ---

// AutomationAction defines a single step in a flow. Exactly one config pointer should be set.
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
	SystemPrompt  string                        `json:"system_prompt,omitempty"`
	Prompt        string                        `json:"prompt"`
	ProviderType  string                        `json:"provider_type"`
	ProviderID    string                        `json:"provider_id"`
	AllowedTools  []bridgeclient.AllowedToolRef `json:"allowed_tools,omitempty"`
	ExecutionMode string                        `json:"execution_mode,omitempty"`
}

// TeamBotConfig configures a team-scoped automation bot for the flow.
type TeamBotConfig struct {
	TeamID     string   `json:"team_id"`
	ChannelIDs []string `json:"channel_ids,omitempty"`
}

// AutomationFlow mirrors the channel-automation plugin's Flow model.
type AutomationFlow struct {
	ID            string             `json:"id,omitempty"`
	Name          string             `json:"name"`
	Enabled       bool               `json:"enabled"`
	Trigger       AutomationTrigger  `json:"trigger"`
	Actions       []AutomationAction `json:"actions"`
	TeamBotConfig *TeamBotConfig     `json:"team_bot_config,omitempty"`
	CreatedAt     int64              `json:"created_at,omitempty"`
	UpdatedAt     int64              `json:"updated_at,omitempty"`
	CreatedBy     string             `json:"created_by,omitempty"`
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
	AutomationID string `json:"automation_id,omitempty" jsonschema:"The ID of a specific automation to retrieve"`
	ChannelID    string `json:"channel_id,omitempty" jsonschema:"Filter automations by trigger channel ID"`
}

// CreateAutomationArgs represents arguments for the create_automation tool.
type CreateAutomationArgs struct {
	Name          string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled       bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	Trigger       AutomationTrigger  `json:"trigger" jsonschema:"Set exactly one trigger type"`
	Actions       []AutomationAction `json:"actions" jsonschema:"Ordered list of actions to perform when triggered"`
	TeamBotConfig *TeamBotConfig     `json:"team_bot_config,omitempty" jsonschema:"Required when any action uses execution_mode team_bot"`
}

// UpdateAutomationArgs represents arguments for the update_automation tool.
type UpdateAutomationArgs struct {
	AutomationID  string             `json:"automation_id" jsonschema:"The ID of the automation to update,minLength=1"`
	Name          string             `json:"name" jsonschema:"The name of the automation,minLength=1"`
	Enabled       bool               `json:"enabled" jsonschema:"Whether the automation is enabled"`
	Trigger       AutomationTrigger  `json:"trigger" jsonschema:"Set exactly one trigger type"`
	Actions       []AutomationAction `json:"actions" jsonschema:"Ordered list of actions to perform when triggered"`
	TeamBotConfig *TeamBotConfig     `json:"team_bot_config,omitempty" jsonschema:"Required when any action uses execution_mode team_bot"`
}

// DeleteAutomationArgs represents arguments for the delete_automation tool.
type DeleteAutomationArgs struct {
	AutomationID string `json:"automation_id" jsonschema:"The ID of the automation to delete,minLength=1"`
}

// GetAutomationInstructionsArgs represents arguments for the get_automation_instructions tool.
type GetAutomationInstructionsArgs struct{}

// automationToolNames lists all automation tool names for filtering.
var automationToolNames = map[string]bool{
	"list_automations":            true,
	"get_automation_instructions": true,
	"create_automation":           true,
	"update_automation":           true,
	"delete_automation":           true,
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
			Description: `List or get channel automations (trigger-action workflows).
Provide automation_id to get a specific automation, or use optional channel_id to filter by trigger channel.
Returns automation details including trigger configuration and action pipeline.`,
			Schema:   llm.NewJSONSchemaFromStruct[ListAutomationsArgs](),
			Resolver: p.toolListAutomations,
		},
		{
			Name: "get_automation_instructions",
			Description: `Get detailed instructions for creating or updating channel automations.
Call this BEFORE calling create_automation or update_automation to understand the full API,
trigger types, action types, execution modes, and best practices.`,
			Schema:   llm.NewJSONSchemaFromStruct[GetAutomationInstructionsArgs](),
			Resolver: p.toolGetAutomationInstructions,
		},
		{
			Name: "create_automation",
			Description: `Create a channel automation — a trigger-action workflow that fires when events occur.
Requires channel admin (or system admin) permission for the trigger channel.
IMPORTANT: Call get_automation_instructions first for detailed guidance on triggers, actions,
execution modes, and tool configuration. Always confirm with the user before creating.`,
			Schema:   llm.NewJSONSchemaFromStruct[CreateAutomationArgs](),
			Resolver: p.toolCreateAutomation,
		},
		{
			Name: "update_automation",
			Description: `Update an existing channel automation. Replaces the full definition — provide all fields, not just changed ones.
IMPORTANT: Call get_automation_instructions first for detailed guidance. Use list_automations to get
the current definition, then modify and pass the full updated flow. Confirm changes with the user before updating.`,
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

const automationInstructions = `AUTOMATION CREATION AND UPDATE INSTRUCTIONS

AGENT DISCOVERY: For an ai_prompt action with provider_type "agent", use the list_agents tool to discover bots.
Each agent's ID is a 26-character Mattermost user ID — use that value as provider_id in the ai_prompt config.

IMPORTANT WORKFLOW — ALWAYS CONFIRM BEFORE CREATING/UPDATING:
Before calling create_automation or update_automation, you MUST present a plain-language summary
to the user and get their explicit confirmation.

The summary must include:
1. TRIGGER: What event fires this automation and its scope.
2. AI TOOLS AND IDENTITY: Which tools the AI will use, what each one does, and whose identity
   runs the tools. The agent (e.g. @matty) is only the LLM brain — it does NOT determine whose
   permissions or identity execute the tools.
   - execution_mode "team_bot": tools run as the team automation bot (restricted to public
     channels). Say "tools run as the team automation bot" — do NOT say they run as the agent.
   - execution_mode "creator" (default): tools run as the flow creator with their permissions.
   - Without tools, the agent can only generate text — it cannot read Mattermost data or act.
   Always explain what each granted tool does so the user understands the access they are giving.
3. OUTPUT: Where the automation will post results — name the specific channel(s).

Format as a numbered list, then ask the user to confirm. Only call create_automation or
update_automation after the user says yes.

If the user's request is missing details (trigger channel, output channel, which tools),
ask clarifying questions BEFORE presenting the summary.

EXECUTION MODES (execution_mode field on ai_prompt actions):
Each ai_prompt action has an execution_mode that controls whose identity runs the completion:

- "team_bot": Runs as the team automation bot. The bot is restricted to a single team and can
  only access public channels. Use this mode for Mattermost embedded MCP tools
  (server_origin "embedded://mattermost"). The bot has no external MCP server connections.
- "creator" (default): Runs as the flow creator with their identity and MCP connections.
  Required for external MCP servers (e.g. Jira, GitHub) since the bot has no authentication
  with external services.

TEAM BOT CONFIG (team_bot_config, flow-level):
If any action uses execution_mode "team_bot", the flow MUST include a team_bot_config:
  {"team_bot_config": {"team_id": "<team-id>", "channel_ids": ["<public-channel-id>", ...]}}
- team_id: the team the bot belongs to (one bot per team).
- channel_ids: public channels the bot needs access to. The bot cannot be added to private channels.

SPLITTING ACTIONS BY TOOL TYPE:
Mattermost embedded MCP tools and external MCP tools CANNOT run in the same ai_prompt action
because they require different identities. Split them into separate actions:
1. Use an ai_prompt with execution_mode "team_bot" and allowed_tools from "embedded://mattermost"
   for Mattermost operations (reading channels, managing members, searching, etc.).
2. Use a separate ai_prompt with execution_mode "creator" (or omitted) and allowed_tools from
   the external MCP server for external operations (Jira tickets, GitHub issues, etc.).
3. Chain results between actions using {{(index .Steps "prev-action-id").Message}}.

If all tools are from the same source (all Mattermost or all external), a single ai_prompt is fine.

ACTION SELECTION: For each step in the automation, choose the right action type:
- send_message / send_dm: for posting text to channels or users.
- ai_prompt with allowed_tools: for anything else — any step that needs to read data, modify
  state, or interact with Mattermost beyond posting text.

TOOL SUFFICIENCY CHECK (THIS IS VERY IMPORTANT): Before presenting the summary, think through
the automation's task step-by-step and verify the granted tools cover every step the agent will
need to perform. Ask: what data does the agent need to discover, read, or act on — and can it
actually do each of those things with only the tools listed? If any step requires a tool that
isn't included, add it to your recommendation and explain why it's needed.

TRIGGERS: Set exactly one trigger type inside the "trigger" object.
- "message_posted": fires when a human user posts a message in the channel. Bot messages are
  automatically filtered out, so there is no risk of bot-triggered loops. High-traffic channels
  will trigger frequently.
  {"trigger": {"message_posted": {"channel_id": "<channel-id>"}}}
- "schedule": fires on a recurring schedule.
  - interval: Go duration string (minimum "5m"). Examples: "1h" (hourly), "24h" (daily), "168h" (weekly).
  - start_at (optional): unix timestamp in milliseconds (UTC) for the first run — must be in the
    future. The automation fires at this time, then repeats every interval. If omitted, the first
    run happens immediately. Use this to schedule a daily recap at e.g. 9am.
  {"trigger": {"schedule": {"channel_id": "<channel-id>", "interval": "24h", "start_at": 1899936000000}}}
- "membership_changed": fires when a member joins or leaves the channel.
  {"trigger": {"membership_changed": {"channel_id": "<channel-id>"}}}
- "channel_created": fires when any new public channel is created. Note: server-wide — fires for
  every new public channel created by any user.
  {"trigger": {"channel_created": {}}}
- "user_joined_team": fires when a non-bot user joins the specified team.
  {"trigger": {"user_joined_team": {"team_id": "<team-id>"}}}

ACTIONS: Ordered array executed sequentially. Each action has a unique "id" (lowercase alphanumeric
and hyphens only, e.g. "generate-recap" not "generate_recap") and exactly one action config.
Action types:
1. "send_message": Posts a message as a bot.
   {"id": "post", "send_message": {"channel_id": "<ch>", "body": "Hello!", "reply_to_post_id": "<optional post id>", "as_bot_id": "<agent-user-id>"}}
   - as_bot_id: the Mattermost user ID of the bot to post as. Must be a bot account. Use
     list_agents to find bot IDs. When chaining a send_message after an ai_prompt action, ALWAYS
     set as_bot_id to the agent's user ID (the same provider_id from the ai_prompt) so the
     message appears to come from the correct bot. If omitted, the message is posted as a generic
     automation bot which is usually not what the user wants.
2. "ai_prompt": Runs an AI agent with a prompt and optional tools. With tools, the agent can
   perform actions (e.g. modify channels, manage members, search) — not just generate text. Does
   NOT post a message — chain a send_message or send_dm action after to post the response.
   {"id": "ask", "ai_prompt": {"prompt": "...", "provider_type": "agent", "provider_id": "<agent-user-id>", "system_prompt": "...", "execution_mode": "team_bot", "allowed_tools": [{"server_origin": "embedded://mattermost", "name": "<tool name>"}]}}
   - provider_type: "agent" (a bot) or "service" (a raw LLM service)
   - provider_id: the agent's Mattermost user ID (26-char ID). Call list_agents to discover
     available agents and their IDs.
   - system_prompt (optional): system instructions for the AI
   - execution_mode (optional): "team_bot" or "creator" (default). See EXECUTION MODES above.
   - allowed_tools: list of {"server_origin","name"} objects the AI agent is allowed to call
     (must match bridge/agent tools discovery exactly). WITHOUT this, the agent has NO tool access
     and can only generate text. IMPORTANT: Only include tools the user has explicitly agreed to.
     Always explain what each tool does in your summary. Prefer the minimum set of tools needed.
   TOOL ORIGINS:
   - server_origin "embedded://mattermost" for Mattermost embedded MCP tools (must use team_bot mode)
   - server_origin "" for built-in tools without an MCP origin
   - Remote MCP servers use the configured server BaseURL as origin (must use creator mode)
   TOOL SELECTION: Use bridge agent tools discovery or list_tools; copy server_origin and name
   from the response — do not guess origins from server display names.
   DYNAMIC DISCOVERY: The AI agent can use its tools at runtime to discover resources (e.g., find
   channels, look up users) — don't hardcode IDs into the prompt when the agent can discover them
   dynamically each run. This keeps automations resilient to changes.
   NOTE: "web_search" is NOT a valid tool name in allowed_tools. Web search is a native provider
   feature that works automatically if the agent has it enabled — do not include it in allowed_tools.
3. "send_dm": Sends a direct message to a user as a bot. Creates the DM channel automatically
   if it doesn't exist.
   {"id": "welcome", "send_dm": {"user_id": "{{.Trigger.User.Id}}", "body": "Welcome!", "as_bot_id": "<bot-user-id>"}}
   - user_id (required): the Mattermost user ID to DM. Supports template syntax.
   - body (required): the message content. Supports template syntax.
   - as_bot_id (required): the bot user ID to send the DM as. Use list_agents to find bot IDs.

TEMPLATE SYNTAX: body, channel_id, reply_to_post_id, prompt, and system_prompt support Go
text/template with this context:
- {{.Trigger.Post.Message}}, {{.Trigger.Post.Id}}, {{.Trigger.Post.ChannelId}}
- {{.Trigger.Channel.Id}}, {{.Trigger.Channel.Name}}, {{.Trigger.Channel.DisplayName}}
- {{.Trigger.User.Id}}, {{.Trigger.User.Username}}, {{.Trigger.User.FirstName}}, {{.Trigger.User.LastName}}
- {{.Trigger.Team.Id}}, {{.Trigger.Team.Name}}, {{.Trigger.Team.DisplayName}}, {{.Trigger.Team.DefaultChannelId}}
- {{(index .Steps "prev-action-id").Message}}, {{(index .Steps "prev-action-id").PostID}} — output from a previous action

CHAINING ACTIONS: Within a single execution mode, a single ai_prompt action can call tools
multiple times AND generate a text response in one step — prefer consolidating related work into
one ai_prompt rather than splitting into many actions. Only split into separate ai_prompt actions
when you need different execution modes (team_bot vs creator).
Use {{(index .Steps "prev-action-id").Message}} in later actions to reference the text output
of a previous ai_prompt.`

func (p *MattermostToolProvider) toolGetAutomationInstructions(_ *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetAutomationInstructionsArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_automation_instructions: %w", err)
	}
	return automationInstructions, nil
}

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
		if !model.IsValidId(args.AutomationID) {
			return "invalid automation_id", fmt.Errorf("invalid automation_id")
		}
		return p.getAutomationByID(ctx, mcpContext, args.AutomationID)
	}

	// Use server-side channel_id filter if provided, otherwise fetch all.
	flowsURL := p.automationAPIURL("/flows")
	if args.ChannelID != "" {
		flowsURL += "?channel_id=" + url.QueryEscape(args.ChannelID)
	}

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodGet, flowsURL, "")
	if err != nil {
		return handleAutomationHTTPError(resp, err, "")
	}
	defer resp.Body.Close()

	var flows []AutomationFlow
	if err := json.NewDecoder(resp.Body).Decode(&flows); err != nil {
		return "failed to parse automation list", fmt.Errorf("failed to decode automations response: %w", err)
	}

	if len(flows) == 0 {
		return "No automations found matching the specified criteria.", nil
	}

	return formatAutomationFlows(flows), nil
}

func (p *MattermostToolProvider) getAutomationByID(ctx context.Context, mcpContext *MCPToolContext, id string) (string, error) {
	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodGet, p.automationAPIURL("/flows/"+id), "")
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

	if msg, err := validateTrigger(args.Trigger); err != nil {
		return msg, err
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	flow := AutomationFlow{
		Name:          args.Name,
		Enabled:       args.Enabled,
		Trigger:       args.Trigger,
		Actions:       args.Actions,
		TeamBotConfig: args.TeamBotConfig,
	}

	body, err := json.Marshal(flow)
	if err != nil {
		return "failed to encode automation", fmt.Errorf("failed to marshal automation: %w", err)
	}

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodPost, p.automationAPIURL("/flows"), string(body))
	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		p.logger.Error("Automation creation failed",
			"status", statusCode,
			"error", err.Error(),
		)
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

	if !model.IsValidId(args.AutomationID) {
		return "invalid automation_id", fmt.Errorf("invalid automation_id")
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	flow := AutomationFlow{
		ID:            args.AutomationID,
		Name:          args.Name,
		Enabled:       args.Enabled,
		Trigger:       args.Trigger,
		Actions:       args.Actions,
		TeamBotConfig: args.TeamBotConfig,
	}

	body, err := json.Marshal(flow)
	if err != nil {
		return "failed to encode automation", fmt.Errorf("failed to marshal automation: %w", err)
	}

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodPut, p.automationAPIURL("/flows/"+args.AutomationID), string(body))
	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		p.logger.Error("Automation update failed",
			"status", statusCode,
			"error", err.Error(),
		)
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

	if !model.IsValidId(args.AutomationID) {
		return "invalid automation_id", fmt.Errorf("invalid automation_id")
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	ctx := context.Background()

	resp, err := doAutomationRequest(ctx, mcpContext.Client, http.MethodDelete, p.automationAPIURL("/flows/"+args.AutomationID), "")
	if err != nil {
		return handleAutomationHTTPError(resp, err, args.AutomationID)
	}
	defer resp.Body.Close()

	return fmt.Sprintf("Successfully deleted automation with ID '%s'.", args.AutomationID), nil
}

// --- Helpers ---

// validateTrigger checks that exactly one trigger type is set.
func validateTrigger(t AutomationTrigger) (string, error) {
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
	if t.UserJoinedTeam != nil {
		count++
	}
	if count == 0 {
		return "trigger is required — set exactly one trigger type", fmt.Errorf("no trigger type set")
	}
	if count > 1 {
		return "trigger must have exactly one type set — got multiple", fmt.Errorf("trigger must have exactly one type set")
	}
	return "", nil
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
// The Mattermost client's DoAPIRequestWithHeaders consumes the response body for non-2xx
// status codes, so resp.Body is typically empty. The original body content is available
// in the err parameter via AppErrorFromJSON.
func handleAutomationHTTPError(resp *http.Response, err error, automationID string) (string, error) {
	if resp == nil {
		return "Channel Automation plugin is not installed or not reachable.", fmt.Errorf("automation plugin request failed: %w", err)
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
		return fmt.Sprintf("Bad request: %s", detail), fmt.Errorf("automation API returned 400: %s", detail)
	case http.StatusUnauthorized, http.StatusForbidden:
		return "You don't have permission to manage automations for this channel.", fmt.Errorf("automation API returned %d: %s", resp.StatusCode, detail)
	case http.StatusNotFound:
		if automationID != "" {
			return fmt.Sprintf("Automation not found with ID '%s'.", automationID), fmt.Errorf("automation API returned 404 for ID %s", automationID)
		}
		return "Channel Automation plugin is not installed or not reachable.", fmt.Errorf("automation API returned 404: %s", detail)
	default:
		return "Channel Automation plugin is not installed or not reachable.", fmt.Errorf("automation API returned %d: %s", resp.StatusCode, detail)
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
				if a.SendMessage.AsBotID != "" {
					result.WriteString(fmt.Sprintf(", as_bot_id=%s", a.SendMessage.AsBotID))
				}
				if a.SendMessage.Body != "" {
					result.WriteString(fmt.Sprintf(", body=%s", a.SendMessage.Body))
				}
			}
			if a.AIPrompt != nil {
				if a.AIPrompt.ExecutionMode != "" {
					result.WriteString(fmt.Sprintf(", execution_mode=%s", a.AIPrompt.ExecutionMode))
				}
				if a.AIPrompt.Prompt != "" {
					result.WriteString(fmt.Sprintf(", prompt=%s", a.AIPrompt.Prompt))
				}
				if a.AIPrompt.SystemPrompt != "" {
					result.WriteString(fmt.Sprintf(", system_prompt=%s", a.AIPrompt.SystemPrompt))
				}
				if len(a.AIPrompt.AllowedTools) > 0 {
					result.WriteString(fmt.Sprintf(", allowed_tools=%v", a.AIPrompt.AllowedTools))
				}
			}
			result.WriteString(")\n")
		}
	}

	if f.TeamBotConfig != nil {
		result.WriteString(fmt.Sprintf("TeamBotConfig: team_id=%s", f.TeamBotConfig.TeamID))
		if len(f.TeamBotConfig.ChannelIDs) > 0 {
			result.WriteString(fmt.Sprintf(", channel_ids=%v", f.TeamBotConfig.ChannelIDs))
		}
		result.WriteString("\n")
	}

	return result.String()
}
