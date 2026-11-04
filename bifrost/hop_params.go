// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	providerutils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// hopRequestKey carries a request's hopRequest rebuilder on its Bifrost
// context. A Bifrost client is shared by every agent on a service, so the
// request being served travels with it, not the plugin.
type hopRequestKey struct{}

// hopRequest rebuilds one fallback attempt's request for that attempt's
// provider and model.
type hopRequest func(req *schemas.BifrostRequest) error

// hopRequestPlugin gives every fallback attempt a request built for its own
// provider and model. Bifrost retries a fallback with the primary's built
// request and only swaps the provider and model, but the body is
// provider-specific: image limits, strict message rules, prompt caching, a
// primary's output limit that can exceed what a fallback model accepts, and
// reasoning (Opus 5.5 rejects the thinking:{type:"disabled"} that Sonnet 5
// needs, Opus 4.6 rejects xhigh, and an effort meant to minimize an always-on
// model's thinking turns thinking on for a budget-based or Gemini fallback).
type hopRequestPlugin struct{}

func (hopRequestPlugin) GetName() string { return "mattermost-ai-hop-request" }

func (hopRequestPlugin) Cleanup() error { return nil }

func (hopRequestPlugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	return nil
}

// PreLLMHook runs once per attempt, with a fallback's provider and model
// already swapped in. The primary's request is built by the request builders,
// so only fallbacks are rebuilt; an attempt whose index is unknown is rebuilt
// too, since the rebuilt request is correct for any hop.
func (hopRequestPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if index, ok := ctx.Value(schemas.BifrostContextKeyFallbackIndex).(int); ok && index == 0 {
		return req, nil, nil
	}
	if rebuild, ok := ctx.Value(hopRequestKey{}).(hopRequest); ok && req != nil {
		return req, nil, rebuild(req)
	}
	return req, nil, nil
}

func (hopRequestPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}

// withHopRequest attaches the per-attempt rebuilder for request as
// requested, before any clamp to a service's or model's output limit.
func (b *LLM) withHopRequest(ctx *schemas.BifrostContext, request llm.CompletionRequest, cfg llm.LanguageModelConfig) {
	ctx.SetValue(hopRequestKey{}, hopRequest(func(req *schemas.BifrostRequest) error {
		return b.applyHopRequest(req, request, cfg)
	}))
}

// applyHopRequest replaces the attempt's input and parameters with ones built
// for its provider and model. Bifrost's fallback copy is shallow, so the input
// and parameters are shared across attempts and must be replaced rather than
// modified.
func (b *LLM) applyHopRequest(req *schemas.BifrostRequest, request llm.CompletionRequest, cfg llm.LanguageModelConfig) error {
	switch {
	case req.ChatRequest != nil:
		hop := *req.ChatRequest
		hop.Input, hop.Params = b.chatRequestForHop(request, cfg, hop.Provider, hop.Model)
		req.ChatRequest = &hop
	case req.ResponsesRequest != nil:
		hop := *req.ResponsesRequest
		input, params, err := b.responsesRequestForHop(request, cfg, hop.Provider, hop.Model)
		if err != nil {
			return err
		}
		hop.Input, hop.Params = input, params
		req.ResponsesRequest = &hop
	case req.CountTokensRequest != nil:
		// CountTokens strips Params to the function tools, which every hop
		// shares and count_tokens endpoints require; only the input is
		// provider-specific.
		hop := *req.CountTokensRequest
		input, _, err := b.responsesRequestForHop(request, cfg, hop.Provider, hop.Model)
		if err != nil {
			return err
		}
		hop.Input = input
		req.CountTokensRequest = &hop
	}
	return nil
}

// chatHopParams returns params with the max output tokens and reasoning block
// for model on the provider registered as name.
func (b *LLM) chatHopParams(params schemas.ChatParameters, name schemas.ModelProvider, model string, cfg llm.LanguageModelConfig) *schemas.ChatParameters {
	cfg = b.configForHop(cfg, name, model)
	params.MaxCompletionTokens = maxTokensParam(cfg)
	params.Reasoning = b.chatReasoningFor(name, cfg)
	return &params
}

// responsesHopParams is chatHopParams for the Responses API.
func (b *LLM) responsesHopParams(params schemas.ResponsesParameters, name schemas.ModelProvider, model string, cfg llm.LanguageModelConfig) *schemas.ResponsesParameters {
	cfg = b.configForHop(cfg, name, model)
	params.MaxOutputTokens = maxTokensParam(cfg)
	params.Reasoning = b.responsesReasoningFor(name, cfg)
	return &params
}

// configForHop returns cfg for model on the provider registered as name, with
// the requested MaxGeneratedTokens clamped to the model's output limit and,
// for a fallback, to its service's configured limit. Clamping from the requested value keeps a
// fallback with a higher limit than the primary from being capped by the
// primary's.
func (b *LLM) configForHop(cfg llm.LanguageModelConfig, name schemas.ModelProvider, model string) llm.LanguageModelConfig {
	cfg.Model = model
	if entry := b.registration(name); entry != nil && entry.outputTokenLimit > 0 {
		cfg.MaxGeneratedTokens = min(cfg.MaxGeneratedTokens, entry.outputTokenLimit)
	}
	cfg.MaxGeneratedTokens = clampToModelOutputLimit(model, cfg.MaxGeneratedTokens)
	return cfg
}

// clampToModelOutputLimit lowers requested to model's max output tokens.
// Bifrost sends max tokens as given and the provider rejects a value above the
// model's limit. Models without a known limit keep requested.
func clampToModelOutputLimit(model string, requested int) int {
	return min(requested, providerutils.GetMaxOutputTokensOrDefault(model, requested))
}

// maxTokensParam returns the max output tokens parameter for cfg, or nil
// when none should be sent.
func maxTokensParam(cfg llm.LanguageModelConfig) *int {
	if cfg.MaxGeneratedTokens <= 0 {
		return nil
	}
	return new(cfg.MaxGeneratedTokens)
}

// baseProvider returns the provider type behind a Bifrost registration name.
func (b *LLM) baseProvider(name schemas.ModelProvider) schemas.ModelProvider {
	if entry := b.registration(name); entry != nil {
		return entry.Provider
	}
	return name
}

// registration returns the service registered under name, or nil when no
// service is.
func (b *LLM) registration(name schemas.ModelProvider) *providerAccount {
	if b.account == nil {
		return nil
	}
	return b.account.entries[name]
}
