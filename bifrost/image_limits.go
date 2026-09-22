// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Documented hard-reject pixel limits for vision input. Bifrost's model catalog
// ships token limits and image-generation pricing tiers, not input-image
// dimension caps, so these live here.
//
// These are rejection ceilings, not the sizes the model actually processes.
// Providers that downscale (Gemini, Cohere, Mistral) return 0 so we do not
// omit images they would accept.
const (
	// https://platform.claude.com/docs/en/build-with-claude/vision
	anthropicMaxImageDimension  = 8000
	anthropicManyImageDimension = 2000
	anthropicManyImageThreshold = 20

	// https://developers.openai.com/api/docs/guides/images-vision
	// Newest models scale any longer side down to this before the separate
	// 30k-patch reject. A single pixel number cannot express that patch cap.
	openaiMaxImageDimension = 65535
)

func baseProvider(provider schemas.ModelProvider) schemas.ModelProvider {
	name := string(provider)
	if i := strings.Index(name, "::"); i >= 0 {
		return schemas.ModelProvider(name[:i])
	}
	return provider
}

// maxImageDimension is the per-side pixel limit above which we omit an image
// rather than send it upstream. Zero means the provider has no documented
// pixel reject and we do not omit on dimensions.
func maxImageDimension(provider schemas.ModelProvider, imageCount int) int {
	switch baseProvider(provider) {
	case schemas.Anthropic, schemas.Bedrock, schemas.BedrockMantle:
		if imageCount > anthropicManyImageThreshold {
			return anthropicManyImageDimension
		}
		return anthropicMaxImageDimension
	case schemas.OpenAI, schemas.Azure:
		return openaiMaxImageDimension
	default:
		return 0
	}
}

func countRequestImages(posts []llm.Post) int {
	n := 0
	for _, post := range posts {
		for _, file := range post.Files {
			if llm.IsSupportedImageMimeType(file.MimeType) {
				n++
			}
		}
	}
	return n
}
