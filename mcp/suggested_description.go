// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

// Where a suggested description was taken from in the server's initialize
// response.
const (
	SuggestedDescriptionSourceDescription  = "description"
	SuggestedDescriptionSourceInstructions = "instructions"
)

// SuggestedDescription is a draft of a remote MCP server's admin-written
// description. Source is empty when the server describes itself in neither
// way.
type SuggestedDescription struct {
	Text   string
	Source string
}

// SuggestRemoteServerDescription connects to a remote MCP server as userID
// and drafts a description from its initialize response: the server's own
// description, else its instructions, flattened and capped to fit
// config.MaxMCPServerDescriptionRunes. The draft is only returned to the
// requesting admin, who decides what to save; nothing is cached, so another
// identity's view of the server never reaches other users.
func SuggestRemoteServerDescription(ctx context.Context, userID string, serverConfig ServerConfig, log pluginapi.LogService, oauthManager *OAuthManager, httpClient *http.Client) (SuggestedDescription, error) {
	client, err := NewClient(ctx, userID, serverConfig, log, oauthManager, httpClient, nil, false)
	if err != nil {
		return SuggestedDescription{}, err
	}
	defer client.Close()

	result := client.session.InitializeResult()
	if result == nil {
		return SuggestedDescription{}, nil
	}
	if result.ServerInfo != nil && strings.TrimSpace(result.ServerInfo.Description) != "" {
		return SuggestedDescription{
			Text:   promptSafeText(result.ServerInfo.Description, config.MaxMCPServerDescriptionRunes),
			Source: SuggestedDescriptionSourceDescription,
		}, nil
	}
	if strings.TrimSpace(result.Instructions) != "" {
		return SuggestedDescription{
			Text:   promptSafeText(result.Instructions, config.MaxMCPServerDescriptionRunes),
			Source: SuggestedDescriptionSourceInstructions,
		}, nil
	}
	return SuggestedDescription{}, nil
}
