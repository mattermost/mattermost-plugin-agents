// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetBotArgs represents arguments for the get_bot tool.
type GetBotArgs struct {
	BotUserID string `json:"bot_user_id" jsonschema:"The bot account's user ID,minLength=26,maxLength=26"`
}

// ListBotsArgs represents arguments for the list_bots tool.
type ListBotsArgs struct {
	Page    int `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int `json:"per_page,omitempty" jsonschema:"Bots per page (default: 50, max: 200),minimum=1,maximum=200"`
}

// ListIncomingWebhooksArgs represents arguments for the list_incoming_webhooks tool.
type ListIncomingWebhooksArgs struct {
	Page    int `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int `json:"per_page,omitempty" jsonschema:"Webhooks per page (default: 50, max: 200),minimum=1,maximum=200"`
}

// ListOutgoingWebhooksArgs represents arguments for the list_outgoing_webhooks tool.
type ListOutgoingWebhooksArgs struct {
	TeamID    string `json:"team_id,omitempty" jsonschema:"Optional team ID to scope the webhooks,maxLength=26"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"Optional channel ID to scope the webhooks,maxLength=26"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Webhooks per page (default: 50, max: 200),minimum=1,maximum=200"`
}

const (
	getBotDescription               = "Get a single bot account's details. Parameters: bot_user_id (required)."
	listBotsDescription             = "List bot accounts. Parameters: page, per_page."
	listIncomingWebhooksDescription = "List incoming webhooks. Parameters: page, per_page."
	listOutgoingWebhooksDescription = "List outgoing webhooks for a team or channel. Parameters: team_id (optional), channel_id (optional), page, per_page."
)

// getIntegrationTools returns the bot and webhook tools.
func (p *MattermostToolProvider) getIntegrationTools() []MCPTool {
	return []MCPTool{
		{Name: "get_bot", Description: getBotDescription, Schema: NewJSONSchemaForAccessMode[GetBotArgs](string(p.accessMode)), Resolver: typed("get_bot", p.toolGetBot)},
		{Name: "list_bots", Description: listBotsDescription, Schema: NewJSONSchemaForAccessMode[ListBotsArgs](string(p.accessMode)), Resolver: typed("list_bots", p.toolListBots)},
		{Name: "list_incoming_webhooks", Description: listIncomingWebhooksDescription, Schema: NewJSONSchemaForAccessMode[ListIncomingWebhooksArgs](string(p.accessMode)), Resolver: typed("list_incoming_webhooks", p.toolListIncomingWebhooks)},
		{Name: "list_outgoing_webhooks", Description: listOutgoingWebhooksDescription, Schema: NewJSONSchemaForAccessMode[ListOutgoingWebhooksArgs](string(p.accessMode)), Resolver: typed("list_outgoing_webhooks", p.toolListOutgoingWebhooks)},
	}
}

// toolGetBot implements the get_bot tool.
func (p *MattermostToolProvider) toolGetBot(mcpContext *MCPToolContext, args GetBotArgs) (string, error) {
	if err := requireID("bot_user_id", args.BotUserID); err != nil {
		return "", err
	}
	bot, _, err := mcpContext.Client.GetBot(mcpContext.Ctx, args.BotUserID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching bot: %w", err)
	}
	var result strings.Builder
	format.WriteBot(&result, format.BotEntry{Bot: bot})
	return result.String(), nil
}

// toolListBots implements the list_bots tool.
func (p *MattermostToolProvider) toolListBots(mcpContext *MCPToolContext, args ListBotsArgs) (string, error) {
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)
	bots, _, err := mcpContext.Client.GetBots(mcpContext.Ctx, page, perPage, "")
	if err != nil {
		return "", fmt.Errorf("error fetching bots: %w", err)
	}
	if len(bots) == 0 {
		return "no bots found", nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d bot(s):\n\n", len(bots)))
	for i, bot := range bots {
		format.WriteBot(&result, format.BotEntry{HeaderLabel: fmt.Sprintf("Bot %d", i+1), Bot: bot})
	}
	return result.String(), nil
}

// toolListIncomingWebhooks implements the list_incoming_webhooks tool.
func (p *MattermostToolProvider) toolListIncomingWebhooks(mcpContext *MCPToolContext, args ListIncomingWebhooksArgs) (string, error) {
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)
	hooks, _, err := mcpContext.Client.GetIncomingWebhooks(mcpContext.Ctx, page, perPage, "")
	if err != nil {
		return "", fmt.Errorf("error fetching incoming webhooks: %w", err)
	}
	if len(hooks) == 0 {
		return "no incoming webhooks found", nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d incoming webhook(s):\n\n", len(hooks)))
	for i, hook := range hooks {
		format.WriteIncomingWebhook(&result, format.IncomingWebhookEntry{HeaderLabel: fmt.Sprintf("Webhook %d", i+1), Webhook: hook})
	}
	return result.String(), nil
}

// toolListOutgoingWebhooks implements the list_outgoing_webhooks tool.
func (p *MattermostToolProvider) toolListOutgoingWebhooks(mcpContext *MCPToolContext, args ListOutgoingWebhooksArgs) (string, error) {
	if err := optionalID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := optionalID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var hooks []*model.OutgoingWebhook
	var err error
	switch {
	case args.ChannelID != "":
		hooks, _, err = client.GetOutgoingWebhooksForChannel(ctx, args.ChannelID, page, perPage, "")
	case args.TeamID != "":
		hooks, _, err = client.GetOutgoingWebhooksForTeam(ctx, args.TeamID, page, perPage, "")
	default:
		hooks, _, err = client.GetOutgoingWebhooks(ctx, page, perPage, "")
	}
	if err != nil {
		return "", fmt.Errorf("error fetching outgoing webhooks: %w", err)
	}
	if len(hooks) == 0 {
		return "no outgoing webhooks found", nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d outgoing webhook(s):\n\n", len(hooks)))
	for i, hook := range hooks {
		format.WriteOutgoingWebhook(&result, format.OutgoingWebhookEntry{HeaderLabel: fmt.Sprintf("Webhook %d", i+1), Webhook: hook})
	}
	return result.String(), nil
}
