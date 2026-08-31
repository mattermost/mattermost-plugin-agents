// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"maps"
	"runtime"
	"slices"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// agentLLMEntry is one agent language model plus the shutdown that releases its
// Bifrost client. EnsureBots retires the previous entry when it rebuilds; the
// client is shut down once every in-flight holder has dropped it.
type agentLLMEntry struct {
	shutdown func()

	// inUse counts the handle stored on a Bot, copies of that handle held by
	// in-flight callers, and in-progress ChatCompletion streams. Retired
	// entries shut down once it drains.
	inUse sync.WaitGroup

	shutdownOnce sync.Once
}

func (e *agentLLMEntry) shutdownNow() {
	e.shutdownOnce.Do(func() {
		if e.shutdown != nil {
			e.shutdown()
		}
	})
}

// agentLLMHandle is the LanguageModel stored on a Bot and returned by LLM().
// A WaitGroup lease is taken for the handle's lifetime so a caller that
// already holds bot.LLM() (toolrunner, streaming, title generation) keeps the
// underlying Bifrost client alive across EnsureBots replacing the bot slice.
// runtime.AddCleanup drops that lease when the handle becomes unreachable;
// ChatCompletion also holds a lease until the returned stream is consumed so
// a one-liner `bot.LLM().ChatCompletion(...)` cannot be collected mid-stream.
type agentLLMHandle struct {
	inner llm.LanguageModel
	entry *agentLLMEntry
}

func newAgentLLMHandle(inner llm.LanguageModel, entry *agentLLMEntry) *agentLLMHandle {
	h := &agentLLMHandle{inner: inner, entry: entry}
	entry.inUse.Add(1)
	runtime.AddCleanup(h, func(e *agentLLMEntry) {
		e.inUse.Done()
	}, entry)
	return h
}

func (h *agentLLMHandle) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	h.entry.inUse.Add(1)
	result, err := h.inner.ChatCompletion(ctx, request, opts...)
	if err != nil {
		h.entry.inUse.Done()
		return nil, err
	}
	return releaseStreamWhenConsumed(result, h.entry.inUse.Done), nil
}

func (h *agentLLMHandle) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	h.entry.inUse.Add(1)
	defer h.entry.inUse.Done()
	return h.inner.ChatCompletionNoStream(ctx, request, opts...)
}

func (h *agentLLMHandle) CountTokens(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	h.entry.inUse.Add(1)
	defer h.entry.inUse.Done()
	return h.inner.CountTokens(ctx, request, opts...)
}

func (h *agentLLMHandle) InputTokenLimit() int {
	return h.inner.InputTokenLimit()
}

func (h *agentLLMHandle) OutputTokenLimit() int {
	return h.inner.OutputTokenLimit()
}

func releaseStreamWhenConsumed(result *llm.TextStreamResult, done func()) *llm.TextStreamResult {
	if result == nil {
		done()
		return nil
	}
	out := make(chan llm.TextStreamEvent)
	go func() {
		defer done()
		defer close(out)
		for event := range result.Stream {
			out <- event
		}
	}()
	return &llm.TextStreamResult{Stream: out}
}

// retireAgentLLM stops handing entry out and shuts it down once its holders
// drain. It must be called only after entry is unreachable from b.bots, so
// that no new Bot.LLM() can obtain this handle: that is what makes inUse.Add
// (on an existing handle) and inUse.Wait mutually safe. A handle that is
// still referenced — including a stale *Bot from before this EnsureBots —
// keeps the lease until it becomes unreachable.
func (b *MMBots) retireAgentLLM(entry *agentLLMEntry) {
	if entry == nil {
		return
	}

	b.agentLLMMu.Lock()
	if b.retiredAgentLLMs == nil {
		b.retiredAgentLLMs = make(map[*agentLLMEntry]struct{})
	}
	b.retiredAgentLLMs[entry] = struct{}{}
	b.agentLLMMu.Unlock()

	go func() {
		entry.inUse.Wait()
		entry.shutdownNow()

		b.agentLLMMu.Lock()
		delete(b.retiredAgentLLMs, entry)
		b.agentLLMMu.Unlock()
	}()
}

func (b *MMBots) shutdownAgentLLMEntries(entries []*agentLLMEntry) {
	for _, entry := range entries {
		if entry != nil {
			entry.shutdownNow()
		}
	}
}

// ShutdownAgentLLMs shuts every agent model down, live or retired, regardless
// of outstanding leases. Called on plugin deactivation alongside
// ShutdownServiceLLMs.
func (b *MMBots) ShutdownAgentLLMs() {
	b.botsLock.RLock()
	live := make([]*agentLLMEntry, 0, len(b.bots))
	for _, bot := range b.bots {
		if bot != nil && bot.llmEntry != nil {
			live = append(live, bot.llmEntry)
		}
	}
	b.botsLock.RUnlock()

	b.agentLLMMu.Lock()
	entries := append([]*agentLLMEntry{}, live...)
	entries = append(entries, slices.Collect(maps.Keys(b.retiredAgentLLMs))...)
	clear(b.retiredAgentLLMs)
	b.agentLLMMu.Unlock()

	b.shutdownAgentLLMEntries(entries)
}
