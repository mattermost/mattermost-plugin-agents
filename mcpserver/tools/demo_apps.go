// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// demoAppMIMEType must equal mcp.UIResourceMIMEType in the plugin's mcp
	// package; declared locally so mcpserver keeps not importing that package.
	demoAppMIMEType = "text/html;profile=mcp-app"

	previewPostResourceURI = "ui://mattermost/preview-post.html"

	previewPostDescription = "Preview a Mattermost post as an interactive MCP App card. Parameters: post_id (required). Returns JSON with the post message, author username, channel display name, and create_at timestamp for the companion UI. Example: {\"post_id\": \"8xqzn3pfmtbyfkr9hqbw4hheoa\"}"
)

//go:embed demo_apps/preview_post.html
var previewPostHTML string

// PreviewPostArgs represents arguments for the preview_post demo tool.
type PreviewPostArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post to preview,minLength=26,maxLength=26"`
}

func (p *MattermostToolProvider) getDemoAppTools() []MCPTool {
	return []MCPTool{{
		Name:        "preview_post",
		Description: previewPostDescription,
		Schema:      NewJSONSchemaForAccessMode[PreviewPostArgs](string(p.accessMode)),
		Resolver:    typed("preview_post", p.toolPreviewPost),
		Meta: mcp.Meta{"ui": map[string]any{
			"resourceUri": previewPostResourceURI,
		}},
	}}
}

// registerDemoAppResources registers the ui:// resources for demo tools once.
// Tools themselves are aggregated via mcpTools() when enableDemoApps is set.
func (p *MattermostToolProvider) registerDemoAppResources(mcpServer *mcp.Server) {
	mcpServer.AddResource(&mcp.Resource{
		URI:      previewPostResourceURI,
		Name:     "preview-post-app",
		MIMEType: demoAppMIMEType,
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      previewPostResourceURI,
			MIMEType: demoAppMIMEType,
			Text:     previewPostHTML,
		}}}, nil
	})
}

func (p *MattermostToolProvider) toolPreviewPost(mcpContext *MCPToolContext, args PreviewPostArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	post, _, err := client.GetPost(ctx, args.PostID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching post: %w", err)
	}

	username := ""
	if user, _, userErr := client.GetUser(ctx, post.UserId, ""); userErr == nil && user != nil {
		username = user.Username
	}

	channelDisplayName := ""
	if channel, _, channelErr := client.GetChannel(ctx, post.ChannelId); channelErr == nil && channel != nil {
		channelDisplayName = channel.DisplayName
	}

	return format.MarshalPostPreview(format.PostPreviewEntry{
		PostID:             post.Id,
		Message:            post.Message,
		Username:           username,
		ChannelDisplayName: channelDisplayName,
		CreateAt:           post.CreateAt,
	})
}
