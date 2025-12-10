// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// benchFakeLLM is a minimal LanguageModel implementation for benchmarks.
// It returns a pre-configured stream without any external dependencies.
type benchFakeLLM struct {
	generator StreamGenerator
}

func (f *benchFakeLLM) ChatCompletion(_ CompletionRequest, _ ...LanguageModelOption) (*TextStreamResult, error) {
	return f.generator.Generate(), nil
}

func (f *benchFakeLLM) ChatCompletionNoStream(_ CompletionRequest, _ ...LanguageModelOption) (string, error) {
	result, err := f.ChatCompletion(CompletionRequest{})
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (f *benchFakeLLM) CountTokens(_ string) int {
	return 0
}

func (f *benchFakeLLM) InputTokenLimit() int {
	return 100000
}

// BenchmarkTokenTracking benchmarks the TokenUsageLoggingWrapper performance.
func BenchmarkTokenTracking(b *testing.B) {
	logger, err := CreateTokenLogger()
	if err != nil {
		b.Skip("Could not create token logger:", err)
	}

	scenarios := BenchmarkScenarios()

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
				wrapper := NewTokenUsageLoggingWrapper(fakeLLM, "bench-bot", logger, nil)

				result, err := wrapper.ChatCompletion(CompletionRequest{
					Context: &Context{
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
