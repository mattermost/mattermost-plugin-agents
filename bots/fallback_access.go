// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// AllowedFallbackServiceIDs returns the prefix of primaryServiceID's fallback
// chain the user may use. Walks hops in order and stops at the first deny or
// evaluation error (later hops are not skipped). The primary is not included.
// A resolve error fails closed: callers must not attach the configured chain.
func AllowedFallbackServiceIDs(ctx context.Context, checker *accesscontrol.Checker, userID, primaryServiceID string, getServiceByID func(id string) (llm.ServiceConfig, bool)) ([]string, error) {
	if getServiceByID == nil {
		return nil, nil
	}
	chain, err := llm.ResolveFallbackChain(primaryServiceID, getServiceByID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, svc := range chain {
		if err := checker.CanUseService(ctx, userID, svc.ID); err != nil {
			break
		}
		ids = append(ids, svc.ID)
	}
	return ids, nil
}

// fallbackAccessLLM stamps each user-attributed completion with the prefix of
// fallback service IDs the requesting user may use. Bifrost then attaches only
// that prefix so a denied hop is never attempted.
type fallbackAccessLLM struct {
	inner            llm.LanguageModel
	bots             *MMBots
	primaryServiceID string
}

func newFallbackAccessLLM(inner llm.LanguageModel, bots *MMBots, primaryServiceID string) llm.LanguageModel {
	return &fallbackAccessLLM{inner: inner, bots: bots, primaryServiceID: primaryServiceID}
}

func (w *fallbackAccessLLM) apply(ctx context.Context, request *llm.CompletionRequest) {
	if request.RestrictFallbacks {
		return
	}
	if request.Context == nil || request.Context.RequestingUser == nil || request.Context.RequestingUser.Id == "" {
		return
	}
	ids, err := AllowedFallbackServiceIDs(ctx, w.bots.accessChecker, request.Context.RequestingUser.Id, w.primaryServiceID, w.bots.serviceByID())
	request.RestrictFallbacks = true
	if err != nil {
		if w.bots.pluginAPI != nil {
			w.bots.pluginAPI.Log.Warn("Dropping fallbacks after chain resolution failed", "service_id", w.primaryServiceID, "error", err.Error())
		}
		return
	}
	request.AllowedFallbackServiceIDs = ids
}

func (w *fallbackAccessLLM) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	w.apply(ctx, &request)
	return w.inner.ChatCompletion(ctx, request, opts...)
}

func (w *fallbackAccessLLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	w.apply(ctx, &request)
	return w.inner.ChatCompletionNoStream(ctx, request, opts...)
}

func (w *fallbackAccessLLM) CountTokens(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	w.apply(ctx, &request)
	return w.inner.CountTokens(ctx, request, opts...)
}

func (w *fallbackAccessLLM) InputTokenLimit() int {
	return w.inner.InputTokenLimit()
}

func (w *fallbackAccessLLM) OutputTokenLimit() int {
	return w.inner.OutputTokenLimit()
}
