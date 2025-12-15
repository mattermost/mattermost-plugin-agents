// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"bytes"
	"context"
	"fmt"
	"image/png"

	"github.com/mattermost/mattermost-plugin-ai/gemini"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
	"github.com/mattermost/mattermost/server/public/model"
)

// GenerateImageArgs represents arguments for the generate_image tool
type GenerateImageArgs struct {
	Prompt    string `json:"prompt" jsonschema:"The text description of the image to generate,minLength=1"`
	ServiceID string `json:"service_id,omitempty" jsonschema:"Optional service ID to use for image generation. If not specified, uses the bot's configured image generation service or main service."`
}

// getImageGenerationTools returns image generation tools
func (p *MattermostToolProvider) getImageGenerationTools() []MCPTool {
	// Only provide image generation tool in local access mode for now
	// This is because it requires API keys and service configuration
	if p.accessMode != AccessModeLocal {
		return []MCPTool{}
	}

	return []MCPTool{
		{
			Name:        "generate_image",
			Description: "Generate an image from a text prompt using AI. The generated image will be uploaded to Mattermost and can be attached to posts. Parameters: prompt (required - detailed description of the image to generate), service_id (optional - specific service ID to use, otherwise uses bot's configured image generation service). Supports OpenAI (DALL-E), Gemini (Imagen), and other configured image generation services. Returns the file ID of the generated image that can be used in attachments. Example: {\"prompt\": \"A serene mountain landscape at sunset with a lake in the foreground\"}",
			Schema:      NewJSONSchemaForAccessMode[GenerateImageArgs](string(p.accessMode)),
			Resolver:    p.toolGenerateImage,
		},
	}
}

// toolGenerateImage implements the generate_image tool
func (p *MattermostToolProvider) toolGenerateImage(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GenerateImageArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool generate_image: %w", err)
	}

	ctx := context.Background()
	client := mcpContext.Client

	// Determine which service to use for image generation
	// Priority: 1) explicit service_id in args, 2) bot's image generation service, 3) error
	serviceID := args.ServiceID
	if serviceID == "" {
		serviceID = mcpContext.ImageGenerationServiceID
	}
	if serviceID == "" {
		return "", fmt.Errorf("no image generation service configured. Please specify a service_id or configure an image generation service for this bot")
	}

	// Look up the service configuration
	serviceConfig, found := p.config.GetServiceByID(serviceID)
	if !found {
		return "", fmt.Errorf("service with ID '%s' not found in configuration", serviceID)
	}

	// Generate the image using the appropriate service based on type
	var imageGenerator llm.ImageGenerator
	var closeFunc func() error
	var serviceName string

	switch serviceConfig.Type {
	case llm.ServiceTypeGemini:
		serviceName = "Gemini"
		geminiClient, err := gemini.New(gemini.Config{
			APIKey:               serviceConfig.APIKey,
			ImageGenerationModel: "gemini-2.5-flash-image",
		})
		if err != nil {
			return "", fmt.Errorf("failed to create Gemini client: %w", err)
		}
		imageGenerator = geminiClient
		closeFunc = geminiClient.Close

	case llm.ServiceTypeOpenAI, llm.ServiceTypeAzure, llm.ServiceTypeOpenAICompatible:
		serviceName = "OpenAI"
		// Create OpenAI client for image generation
		openaiConfig := openai.Config{
			APIKey:       serviceConfig.APIKey,
			APIURL:       serviceConfig.APIURL,
			OrgID:        serviceConfig.OrgID,
			DefaultModel: serviceConfig.DefaultModel,
		}

		var openaiClient *openai.OpenAI
		switch serviceConfig.Type {
		case llm.ServiceTypeAzure:
			openaiClient = openai.NewAzure(openaiConfig, nil)
		case llm.ServiceTypeOpenAICompatible:
			openaiClient = openai.NewCompatible(openaiConfig, nil)
		default:
			openaiClient = openai.New(openaiConfig, nil)
		}
		imageGenerator = openaiClient

	default:
		return "", fmt.Errorf("service type '%s' does not support image generation. Supported types: %s, %s, %s, %s",
			serviceConfig.Type, llm.ServiceTypeGemini, llm.ServiceTypeOpenAI, llm.ServiceTypeAzure, llm.ServiceTypeOpenAICompatible)
	}

	// Close client if needed
	if closeFunc != nil {
		defer closeFunc()
	}

	// Generate the image
	p.logger.Debug("Generating image", "service", serviceName, "serviceID", serviceID, "prompt", args.Prompt)
	img, err := imageGenerator.GenerateImage(args.Prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate image with %s: %w", serviceName, err)
	}

	// Convert image to PNG bytes
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	if err != nil {
		return "", fmt.Errorf("failed to encode image as PNG: %w", err)
	}

	// Upload the image to Mattermost
	// If we have a channel context, upload to that channel for easier attachment
	channelID := mcpContext.ChannelID
	fileUploadResponse, _, err := client.UploadFileAsRequestBody(ctx, buf.Bytes(), channelID, fmt.Sprintf("ai_generated_%s.png", serviceConfig.Name))
	if err != nil {
		return "", fmt.Errorf("failed to upload generated image: %w", err)
	}

	if len(fileUploadResponse.FileInfos) == 0 {
		return "", fmt.Errorf("no file info returned from upload")
	}

	fileID := fileUploadResponse.FileInfos[0].Id
	fileURL := fmt.Sprintf("%s/files/%s", p.mmServerURL, fileID)

	p.logger.Debug("Image generated and uploaded successfully", "fileID", fileID, "service", serviceName, "channelID", channelID)

	// Build response message
	response := fmt.Sprintf("Image generated successfully using %s (%s)!\n\nFile ID: %s\nURL: %s", serviceName, serviceConfig.Name, fileID, fileURL)

	// If we have conversation context, let the bot know it can attach this to its response
	if channelID != "" {
		response += "\n\nThe image has been uploaded to the current channel and is ready to be attached to your response. You can include it by adding the file ID to your message."
	} else {
		response += "\n\nYou can attach this image to a post by including the file ID in the 'attachments' parameter when creating a post."
	}

	return response, nil
}
