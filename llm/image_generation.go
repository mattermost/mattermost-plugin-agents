// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "image"

// ImageGenerator defines the interface for services that can generate images from text prompts
type ImageGenerator interface {
	// GenerateImage generates an image from a text prompt
	GenerateImage(prompt string) (image.Image, error)
}
