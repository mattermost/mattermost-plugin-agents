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

// BenchmarkTokenTrackingWrapper_Overhead measures the overhead introduced by
// the TokenUsageLoggingWrapper when intercepting streams.
func BenchmarkTokenTrackingWrapper_Overhead(b *testing.B) {
	scenarios := []struct {
		name         string
		includeUsage bool
	}{
		{"without_usage_events", false},
		{"with_usage_events", true},
	}

	logger, err := CreateTokenLogger()
	if err != nil {
		b.Skip("Could not create token logger:", err)
	}

	for _, sc := range scenarios {
		generator := StreamGenerator{
			TotalTextSize: 4000,
			ChunkSize:     100,
			IncludeUsage:  sc.includeUsage,
		}

		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fakeLLM := &benchFakeLLM{generator: generator}
				wrapper := NewTokenUsageLoggingWrapper(fakeLLM, "bench-bot", logger, nil)

				request := CompletionRequest{
					Context: &Context{
						RequestingUser: &model.User{Id: "user-bench"},
						Team:           &model.Team{Id: "team-bench"},
					},
				}

				result, err := wrapper.ChatCompletion(request)
				if err != nil {
					b.Fatal(err)
				}

				// Consume the wrapped stream
				for range result.Stream {
				}
			}
		})
	}
}

// BenchmarkTokenTrackingWrapper_VsUnwrapped compares wrapped vs unwrapped performance.
func BenchmarkTokenTrackingWrapper_VsUnwrapped(b *testing.B) {
	generator := StreamGenerator{
		TotalTextSize: 4000,
		ChunkSize:     100,
		IncludeUsage:  true,
	}

	b.Run("unwrapped_direct", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			stream := generator.Generate()
			for range stream.Stream {
			}
		}
	})

	logger, err := CreateTokenLogger()
	if err != nil {
		b.Skip("Could not create token logger:", err)
	}

	b.Run("wrapped_with_logging", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
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

			for range result.Stream {
			}
		}
	})
}

// BenchmarkTokenTrackingWrapper_LargeStream tests wrapper performance with large streams.
func BenchmarkTokenTrackingWrapper_LargeStream(b *testing.B) {
	scenarios := StandardBenchmarkScenarios()

	logger, err := CreateTokenLogger()
	if err != nil {
		b.Skip("Could not create token logger:", err)
	}

	for _, sc := range scenarios {
		// Add usage events to all scenarios
		sc.Generator.IncludeUsage = true

		b.Run(sc.Name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fakeLLM := &benchFakeLLM{generator: sc.Generator}
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

				// Read all and verify
				text, err := result.ReadAll()
				if err != nil {
					b.Fatal(err)
				}
				if len(text) != sc.Generator.TotalTextSize {
					b.Fatalf("unexpected text size: got %d, want %d", len(text), sc.Generator.TotalTextSize)
				}
			}
		})
	}
}

// BenchmarkTokenTrackingWrapper_ChannelForwarding measures the overhead of
// creating and forwarding events through the intercepted channel.
func BenchmarkTokenTrackingWrapper_ChannelForwarding(b *testing.B) {
	// Test with varying event counts
	scenarios := []struct {
		name      string
		chunkSize int
		textSize  int
	}{
		{"few_events_10", 400, 4000},      // ~10 events
		{"moderate_events_40", 100, 4000}, // ~40 events
		{"many_events_200", 20, 4000},     // ~200 events
		{"extreme_events_1000", 4, 4000},  // ~1000 events
	}

	logger, err := CreateTokenLogger()
	if err != nil {
		b.Skip("Could not create token logger:", err)
	}

	for _, sc := range scenarios {
		generator := StreamGenerator{
			TotalTextSize: sc.textSize,
			ChunkSize:     sc.chunkSize,
			IncludeUsage:  true,
		}

		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fakeLLM := &benchFakeLLM{generator: generator}
				wrapper := NewTokenUsageLoggingWrapper(fakeLLM, "bench-bot", logger, nil)

				result, err := wrapper.ChatCompletion(CompletionRequest{
					Context: &Context{
						RequestingUser: &model.User{Id: "user-bench"},
					},
				})
				if err != nil {
					b.Fatal(err)
				}

				eventCount := 0
				for range result.Stream {
					eventCount++
				}
			}
		})
	}
}
