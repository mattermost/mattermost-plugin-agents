// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"

	"github.com/mattermost/mattermost-plugin-ai/gemini"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
	"github.com/mattermost/mattermost/server/public/model"
)

// GenerateImageArgs represents arguments for the generate_image tool
type GenerateImageArgs struct {
	Prompt   string `json:"prompt" jsonschema:"The text description of the image to generate,minLength=1"`
	Provider string `json:"provider,omitempty" jsonschema:"Image generation provider to use: 'openai' or 'gemini' (default: gemini)"`
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
			Description: "Generate an image from a text prompt using AI. The generated image will be uploaded to Mattermost and can be attached to posts. Parameters: prompt (required - detailed description of the image to generate), provider (optional - 'openai' for DALL-E or 'gemini' for Gemini image generation, defaults to 'gemini'). Returns the file ID of the generated image that can be used in attachments. Example: {\"prompt\": \"A serene mountain landscape at sunset with a lake in the foreground\", \"provider\": \"gemini\"}",
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

	// Default to gemini if no provider specified
	provider := args.Provider
	if provider == "" {
		provider = "gemini"
	}

	// Generate the image using the specified provider
	var imageGenerator llm.ImageGenerator
	var closeFunc func() error

	switch provider {
	case "gemini":
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			return "", fmt.Errorf("GEMINI_API_KEY environment variable not set")
		}

		geminiClient, err := gemini.New(gemini.Config{
			APIKey:               apiKey,
			ImageGenerationModel: "gemini-2.5-flash-image",
		})
		if err != nil {
			return "", fmt.Errorf("failed to create Gemini client: %w", err)
		}
		imageGenerator = geminiClient
		closeFunc = geminiClient.Close

	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return "", fmt.Errorf("OPENAI_API_KEY environment variable not set")
		}

		// Create OpenAI client for image generation
		// Note: This uses the existing OpenAI implementation
		openaiClient := openai.New(openai.Config{
			APIKey: apiKey,
		}, nil)
		imageGenerator = openaiClient

	default:
		return "", fmt.Errorf("unsupported image generation provider: %s (supported: 'openai', 'gemini')", provider)
	}

	// Close client if needed
	if closeFunc != nil {
		defer closeFunc()
	}

	// Generate the image
	p.logger.Debug("Generating image with provider", "provider", provider, "prompt", args.Prompt)
	img, err := imageGenerator.GenerateImage(args.Prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate image: %w", err)
	}

	// Convert image to PNG bytes
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	if err != nil {
		return "", fmt.Errorf("failed to encode image as PNG: %w", err)
	}

	// Upload the image to Mattermost
	// We'll create a temporary channel ID - in practice, this might need to be smarter
	// For now, we'll just upload to the "system" and return the file ID
	fileUploadResponse, _, err := client.UploadFileAsRequestBody(ctx, buf.Bytes(), "", fmt.Sprintf("generated_image_%s.png", provider))
	if err != nil {
		return "", fmt.Errorf("failed to upload generated image: %w", err)
	}

	if len(fileUploadResponse.FileInfos) == 0 {
		return "", fmt.Errorf("no file info returned from upload")
	}

	fileID := fileUploadResponse.FileInfos[0].Id
	fileURL := fmt.Sprintf("%s/files/%s", p.mmServerURL, fileID)

	p.logger.Debug("Image generated and uploaded successfully", "fileID", fileID, "provider", provider)

	return fmt.Sprintf("Image generated successfully using %s! File ID: %s, URL: %s\n\nYou can now attach this image to a post by including the file ID in the 'attachments' parameter when creating a post.", provider, fileID, fileURL), nil
}
