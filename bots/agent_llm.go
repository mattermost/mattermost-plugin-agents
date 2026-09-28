// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"runtime"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// agentLLMHandle is the LanguageModel stored on a Bot and returned by LLM().
// It holds a lease on the service's shared provider client for as long as the
// handle is reachable, so a caller that already holds bot.LLM() (toolrunner,
// streaming, title generation) keeps that client alive when a service change
// retires it. runtime.AddCleanup drops the lease once the handle becomes
// unreachable; ChatCompletion also holds a lease until the returned stream is
// consumed so a one-liner `bot.LLM().ChatCompletion(...)` cannot be collected
// mid-stream.
type agentLLMHandle struct {
	inner llm.LanguageModel
	entry *serviceLLMEntry
}

// newAgentLLMHandle takes over a lease the caller already holds on entry.
func newAgentLLMHandle(inner llm.LanguageModel, entry *serviceLLMEntry) *agentLLMHandle {
	h := &agentLLMHandle{inner: inner, entry: entry}
	runtime.AddCleanup(h, func(e *serviceLLMEntry) {
		e.inUse.Done()
	}, entry)
	return h
}

// The per-call inUse.Add below must happen while the handle lease is still
// held; runtime.KeepAlive pins h past the Add so the cleanup cannot run first.
func (h *agentLLMHandle) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	h.entry.inUse.Add(1)
	runtime.KeepAlive(h)
	result, err := h.inner.ChatCompletion(ctx, request, opts...)
	if err != nil {
		h.entry.inUse.Done()
		return nil, err
	}
	return releaseStreamWhenConsumed(ctx, result, h.entry.inUse.Done), nil
}

func (h *agentLLMHandle) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	h.entry.inUse.Add(1)
	runtime.KeepAlive(h)
	defer h.entry.inUse.Done()
	return h.inner.ChatCompletionNoStream(ctx, request, opts...)
}

func (h *agentLLMHandle) CountTokens(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	h.entry.inUse.Add(1)
	runtime.KeepAlive(h)
	defer h.entry.inUse.Done()
	return h.inner.CountTokens(ctx, request, opts...)
}

func (h *agentLLMHandle) InputTokenLimit() int {
	return h.inner.InputTokenLimit()
}

func (h *agentLLMHandle) OutputTokenLimit() int {
	return h.inner.OutputTokenLimit()
}

func releaseStreamWhenConsumed(ctx context.Context, result *llm.TextStreamResult, done func()) *llm.TextStreamResult {
	if result == nil {
		done()
		return nil
	}
	out := make(chan llm.TextStreamEvent)
	go func() {
		defer done()
		defer close(out)
		for event := range result.Stream {
			select {
			case out <- event:
			case <-ctx.Done():
				// Consumer stopped (e.g. StreamToPost on cancel). Drop the
				// remainder so the producer can exit, then release the lease.
				for range result.Stream { //nolint:revive // drain only
				}
				return
			}
		}
	}()
	return &llm.TextStreamResult{Stream: out}
}
