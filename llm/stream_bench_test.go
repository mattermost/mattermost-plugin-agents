// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"
)

// BenchmarkReadAll benchmarks the ReadAll() function with varying response sizes.
// This measures the overhead of string concatenation and event processing.
func BenchmarkReadAll(b *testing.B) {
	scenarios := StandardBenchmarkScenarios()

	for _, sc := range scenarios {
		b.Run(sc.Name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				stream := sc.Generator.Generate()
				result, err := stream.ReadAll()
				if err != nil {
					b.Fatal(err)
				}
				if len(result) != sc.Generator.TotalTextSize {
					b.Fatalf("unexpected result size: got %d, want %d", len(result), sc.Generator.TotalTextSize)
				}
			}
		})
	}
}

// BenchmarkReadAll_MixedEvents benchmarks ReadAll with different event type combinations.
// ReadAll should ignore non-text events (reasoning, usage, annotations) efficiently.
func BenchmarkReadAll_MixedEvents(b *testing.B) {
	scenarios := MixedEventScenarios()

	for _, sc := range scenarios {
		// Skip tool_calls scenario since ReadAll returns error for tool calls
		if sc.Name == "with_tool_calls" {
			continue
		}

		b.Run(sc.Name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				stream := sc.Generator.Generate()
				_, err := stream.ReadAll()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkStreamConsumption_RawChannel measures raw channel read speed.
// This provides a baseline for channel overhead without any processing.
func BenchmarkStreamConsumption_RawChannel(b *testing.B) {
	scenarios := StandardBenchmarkScenarios()

	for _, sc := range scenarios {
		b.Run(sc.Name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				stream := sc.Generator.Generate()
				count := 0
				for range stream.Stream {
					count++
				}
				if count == 0 {
					b.Fatal("no events received")
				}
			}
		})
	}
}

// BenchmarkNewStreamFromString benchmarks the simple stream creation function.
func BenchmarkNewStreamFromString(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"small_100b", 100},
		{"medium_1kb", 1000},
		{"large_10kb", 10000},
		{"xlarge_100kb", 100000},
	}

	for _, s := range sizes {
		text := generateBenchText(s.size)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				stream := NewStreamFromString(text)
				result, err := stream.ReadAll()
				if err != nil {
					b.Fatal(err)
				}
				if len(result) != s.size {
					b.Fatalf("unexpected result size: got %d, want %d", len(result), s.size)
				}
			}
		})
	}
}

// BenchmarkEventTypeSwitch measures the overhead of the event type switch statement.
// This isolates the cost of type checking and casting event values.
func BenchmarkEventTypeSwitch(b *testing.B) {
	// Pre-generate events to benchmark just the switch overhead
	events := []TextStreamEvent{
		{Type: EventTypeText, Value: "Hello, this is a text chunk. "},
		{Type: EventTypeText, Value: "Another chunk of text here. "},
		{Type: EventTypeReasoning, Value: "Thinking about the problem..."},
		{Type: EventTypeUsage, Value: TokenUsage{InputTokens: 100, OutputTokens: 50}},
		{Type: EventTypeText, Value: "Final text chunk. "},
		{Type: EventTypeEnd, Value: nil},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var result string
		for _, event := range events {
			switch event.Type {
			case EventTypeText:
				if textChunk, ok := event.Value.(string); ok {
					result += textChunk
				}
			case EventTypeError:
				// Handle error
			case EventTypeEnd:
				// Done
			case EventTypeToolCalls, EventTypeAnnotations, EventTypeReasoning, EventTypeReasoningEnd, EventTypeUsage:
				// Ignored in ReadAll
			}
		}
		_ = result
	}
}
