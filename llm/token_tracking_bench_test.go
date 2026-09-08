// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm_test

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
)

// benchFakeLLM is a minimal LanguageModel implementation for benchmarks.
// It returns a pre-configured stream without any external dependencies.
type benchFakeLLM struct {
	generator llmtest.StreamGenerator
}

func (f *benchFakeLLM) ChatCompletion(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return f.generator.Generate(), nil
}

func (f *benchFakeLLM) ChatCompletionNoStream(ctx context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	result, err := f.ChatCompletion(ctx, llm.CompletionRequest{})
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (f *benchFakeLLM) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}

func (f *benchFakeLLM) InputTokenLimit() int {
	return 100000
}

func (f *benchFakeLLM) OutputTokenLimit() int {
	return 8192
}

// BenchmarkTokenTracking benchmarks the TokenUsageLoggingWrapper performance.
func BenchmarkTokenTracking(b *testing.B) {
	logger, err := llm.CreateTokenLogger()
	if err != nil {
		b.Skip("Could not create token logger:", err)
	}

	scenarios := llmtest.BenchmarkScenarios()

	for _, sc := range scenarios {
		// Skip tool_calls scenario since ReadAll returns error for tool calls
		if sc.Name == "with_tool_calls" {
			continue
		}

		// Add usage events to all scenarios
		generator := sc.Generator
		generator.IncludeUsage = true

		b.Run(sc.Name, func(b *testing.B) {
			for b.Loop() {
				fakeLLM := &benchFakeLLM{generator: generator}
				sinks := llm.NewTokenUsageSinks(nil)
				sinks.SetLoggingEnabled(true)
				sinks.SetPluginEnabled(false)
				sinks.SetFileEnabled(true)
				sinks.SetFileLogger(logger)
				wrapper := llm.NewTokenUsageLoggingWrapper(fakeLLM, "bench-bot", sinks, nil)

				result, err := wrapper.ChatCompletion(context.Background(), llm.CompletionRequest{
					Context: &llm.Context{
						RequestingUser: &model.User{Id: "user-bench"},
						Team:           &model.Team{Id: "team-bench"},
					},
				})
				if err != nil {
					b.Fatal(err)
				}

				text, err := result.ReadAll()
				if err != nil {
					b.Fatal(err)
				}
				if len(text) != generator.TotalTextSize {
					b.Fatalf("unexpected text size: got %d, want %d", len(text), generator.TotalTextSize)
				}
			}
		})
	}
}
