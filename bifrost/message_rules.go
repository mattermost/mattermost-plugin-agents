// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"github.com/maximhq/bifrost/core/schemas"
)

// messageRules are the message rules of one hop: one Bifrost registration
// name serving one model. Each hop's request is built against its own rules.
type messageRules struct {
	// imageDimensionLimit is the per-side pixel limit above which the hop
	// rejects an image, or zero when it rejects none on dimensions.
	imageDimensionLimit int
	// strictMessages is set when the hop rejects replayed reasoning without a
	// provider signature and requires alternating roles (Anthropic, Bedrock,
	// and Claude on Vertex or Azure).
	strictMessages bool
	// promptCaching requests Anthropic automatic prompt caching (top-level
	// cache_control); Anthropic caches nothing unless asked, so every turn
	// would re-bill the whole prompt. It is set only for Anthropic hops:
	// Bifrost forwards the marker unstripped, and other providers reject it.
	promptCaching bool
}

// messageRulesFor returns the rules for model on the provider registered as
// name, for a request carrying imageCount images.
func (b *LLM) messageRulesFor(name schemas.ModelProvider, model string, imageCount int) messageRules {
	base := b.baseProvider(name)
	messagesAPI := messagesAPIProvider(base, model)
	rules := messageRules{
		imageDimensionLimit: maxImageDimension(messagesAPI, imageCount),
		promptCaching:       base == schemas.Anthropic,
	}
	switch messagesAPI {
	case schemas.Anthropic, schemas.Bedrock, schemas.BedrockMantle:
		rules.strictMessages = true
	}
	return rules
}

// messagesAPIProvider returns the provider whose message rules model follows
// on base: Bifrost sends Claude models on Vertex and Azure to Anthropic's
// Messages API.
func messagesAPIProvider(base schemas.ModelProvider, model string) schemas.ModelProvider {
	if (base == schemas.Vertex || base == schemas.Azure) && schemas.IsAnthropicModel(model) {
		return schemas.Anthropic
	}
	return base
}
