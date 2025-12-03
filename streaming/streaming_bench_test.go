// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// streamBenchGenerator creates synthetic streams for streaming benchmarks.
// This is a local copy to avoid import issues with test files.
type streamBenchGenerator struct {
	TotalTextSize      int
	ChunkSize          int
	IncludeReasoning   bool
	IncludeToolCalls   bool
	IncludeUsage       bool
	IncludeAnnotations bool
}

func (g *streamBenchGenerator) generate() *llm.TextStreamResult {
	bufferSize := (g.TotalTextSize / max(g.ChunkSize, 1)) + 10
	stream := make(chan llm.TextStreamEvent, bufferSize)

	go func() {
		defer close(stream)

		// Generate reasoning events first if enabled
		if g.IncludeReasoning {
			reasoningText := generateBenchmarkText(g.TotalTextSize / 2)
			for i := 0; i < len(reasoningText); i += g.ChunkSize {
				end := min(i+g.ChunkSize, len(reasoningText))
				stream <- llm.TextStreamEvent{
					Type:  llm.EventTypeReasoning,
					Value: reasoningText[i:end],
				}
			}
			stream <- llm.TextStreamEvent{
				Type: llm.EventTypeReasoningEnd,
				Value: llm.ReasoningData{
					Text:      reasoningText,
					Signature: "bench-signature-12345",
				},
			}
		}

		// Generate text events
		text := generateBenchmarkText(g.TotalTextSize)
		for i := 0; i < len(text); i += g.ChunkSize {
			end := min(i+g.ChunkSize, len(text))
			stream <- llm.TextStreamEvent{
				Type:  llm.EventTypeText,
				Value: text[i:end],
			}
		}

		// Send annotations if enabled
		if g.IncludeAnnotations {
			stream <- llm.TextStreamEvent{
				Type: llm.EventTypeAnnotations,
				Value: []llm.Annotation{
					{
						Type:      llm.AnnotationTypeURLCitation,
						URL:       "https://example.com/source1",
						Title:     "Example Source 1",
						CitedText: "Some cited text",
						Index:     1,
					},
				},
			}
		}

		// Send usage if enabled
		if g.IncludeUsage {
			stream <- llm.TextStreamEvent{
				Type: llm.EventTypeUsage,
				Value: llm.TokenUsage{
					InputTokens:  int64(g.TotalTextSize / 4),
					OutputTokens: int64(g.TotalTextSize / 4),
				},
			}
		}

		// End with tool calls or regular end
		if g.IncludeToolCalls {
			stream <- llm.TextStreamEvent{
				Type: llm.EventTypeToolCalls,
				Value: []llm.ToolCall{
					{
						ID:        "tc-bench-1",
						Name:      "benchmark_tool",
						Arguments: json.RawMessage(`{"param": "value"}`),
						Status:    llm.ToolCallStatusPending,
					},
				},
			}
		} else {
			stream <- llm.TextStreamEvent{
				Type:  llm.EventTypeEnd,
				Value: nil,
			}
		}
	}()

	return &llm.TextStreamResult{
		Stream: stream,
	}
}

func generateBenchmarkText(size int) string {
	if size <= 0 {
		return ""
	}
	const pattern = "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. "
	var sb strings.Builder
	sb.Grow(size)
	for sb.Len() < size {
		remaining := size - sb.Len()
		if remaining >= len(pattern) {
			sb.WriteString(pattern)
		} else {
			sb.WriteString(pattern[:remaining])
		}
	}
	return sb.String()
}

type streamingBenchScenario struct {
	name      string
	generator streamBenchGenerator
}

func standardStreamingScenarios() []streamingBenchScenario {
	return []streamingBenchScenario{
		{
			name: "small_100_tokens",
			generator: streamBenchGenerator{
				TotalTextSize: 400,
				ChunkSize:     40,
			},
		},
		{
			name: "medium_1k_tokens",
			generator: streamBenchGenerator{
				TotalTextSize: 4000,
				ChunkSize:     100,
			},
		},
		{
			name: "large_8k_tokens",
			generator: streamBenchGenerator{
				TotalTextSize: 32000,
				ChunkSize:     200,
			},
		},
		{
			name: "xlarge_32k_tokens",
			generator: streamBenchGenerator{
				TotalTextSize: 128000,
				ChunkSize:     500,
			},
		},
	}
}

func mixedEventScenarios() []streamingBenchScenario {
	return []streamingBenchScenario{
		{
			name: "text_only",
			generator: streamBenchGenerator{
				TotalTextSize: 4000,
				ChunkSize:     100,
			},
		},
		{
			name: "with_reasoning",
			generator: streamBenchGenerator{
				TotalTextSize:    4000,
				ChunkSize:        100,
				IncludeReasoning: true,
			},
		},
		{
			name: "with_tool_calls",
			generator: streamBenchGenerator{
				TotalTextSize:    4000,
				ChunkSize:        100,
				IncludeToolCalls: true,
			},
		},
		{
			name: "with_annotations",
			generator: streamBenchGenerator{
				TotalTextSize:      4000,
				ChunkSize:          100,
				IncludeAnnotations: true,
			},
		},
		{
			name: "full_realistic",
			generator: streamBenchGenerator{
				TotalTextSize:      8000,
				ChunkSize:          150,
				IncludeReasoning:   true,
				IncludeUsage:       true,
				IncludeAnnotations: true,
			},
		},
	}
}

// BenchmarkStreamToPost benchmarks the core StreamToPost function with varying sizes.
func BenchmarkStreamToPost(b *testing.B) {
	bundle := i18n.Init()
	scenarios := standardStreamingScenarios()

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				client := newBenchmarkClient()
				service := NewMMPostStreamService(client, bundle)

				stream := sc.generator.generate()
				post := &model.Post{
					Id:        "bench-post-id",
					ChannelId: "bench-channel-id",
					Message:   "",
				}

				ctx := context.Background()
				service.StreamToPost(ctx, stream, post, "en")
			}
		})
	}
}

// BenchmarkStreamToPost_MixedEvents benchmarks StreamToPost with different event combinations.
func BenchmarkStreamToPost_MixedEvents(b *testing.B) {
	bundle := i18n.Init()
	scenarios := mixedEventScenarios()

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				client := newBenchmarkClient()
				service := NewMMPostStreamService(client, bundle)

				stream := sc.generator.generate()
				post := &model.Post{
					Id:        "bench-post-id",
					ChannelId: "bench-channel-id",
					Message:   "",
				}

				ctx := context.Background()
				service.StreamToPost(ctx, stream, post, "en")
			}
		})
	}
}

// BenchmarkStreamToPost_WSEventVolume measures WebSocket event publishing overhead.
func BenchmarkStreamToPost_WSEventVolume(b *testing.B) {
	bundle := i18n.Init()

	// Test with varying chunk sizes to change number of WS events
	scenarios := []struct {
		name      string
		chunkSize int
		textSize  int
	}{
		{"few_ws_events_10", 400, 4000},
		{"moderate_ws_events_40", 100, 4000},
		{"many_ws_events_200", 20, 4000},
	}

	for _, sc := range scenarios {
		generator := streamBenchGenerator{
			TotalTextSize: sc.textSize,
			ChunkSize:     sc.chunkSize,
		}

		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				client := newBenchmarkClient()
				service := NewMMPostStreamService(client, bundle)

				stream := generator.generate()
				post := &model.Post{
					Id:        "bench-post-id",
					ChannelId: "bench-channel-id",
					Message:   "",
				}

				ctx := context.Background()
				service.StreamToPost(ctx, stream, post, "en")

				// Verify expected WS event count (each text chunk + start + end)
				expectedEvents := (sc.textSize / sc.chunkSize) + 2 // +2 for start and end
				actualEvents := client.GetWSEventCount()
				if actualEvents < int64(expectedEvents-1) {
					b.Fatalf("unexpected WS event count: got %d, want ~%d", actualEvents, expectedEvents)
				}
			}
		})
	}
}

// BenchmarkStreamToPost_ContextCancellation benchmarks behavior when context is cancelled.
func BenchmarkStreamToPost_ContextCancellation(b *testing.B) {
	bundle := i18n.Init()

	generator := streamBenchGenerator{
		TotalTextSize: 4000,
		ChunkSize:     100,
	}

	b.Run("immediate_cancel", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			client := newBenchmarkClient()
			service := NewMMPostStreamService(client, bundle)

			stream := generator.generate()
			post := &model.Post{
				Id:        "bench-post-id",
				ChannelId: "bench-channel-id",
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // Cancel immediately
			service.StreamToPost(ctx, stream, post, "en")
		}
	})
}

// BenchmarkStreamToPost_MeasureLatency collects detailed timing metrics.
func BenchmarkStreamToPost_MeasureLatency(b *testing.B) {
	bundle := i18n.Init()

	generator := streamBenchGenerator{
		TotalTextSize: 4000,
		ChunkSize:     100,
	}

	var totalFirstTokenLatency time.Duration
	var totalP95Latency time.Duration
	validSamples := 0

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		client := newBenchmarkClient()
		service := NewMMPostStreamService(client, bundle)

		stream := generator.generate()
		post := &model.Post{
			Id:        "bench-post-id",
			ChannelId: "bench-channel-id",
		}

		startTime := time.Now()
		ctx := context.Background()
		service.StreamToPost(ctx, stream, post, "en")

		// Collect metrics
		firstWSTime := client.GetFirstWSTime()
		if !firstWSTime.IsZero() {
			totalFirstTokenLatency += firstWSTime.Sub(startTime)
			validSamples++
		}
		p95 := client.GetLatencyP95()
		if p95 > 0 {
			totalP95Latency += p95
		}
	}

	// Report custom metrics
	if validSamples > 0 {
		b.ReportMetric(float64(totalFirstTokenLatency.Nanoseconds())/float64(validSamples), "ns/first_token")
	}
	if b.N > 0 {
		b.ReportMetric(float64(totalP95Latency.Nanoseconds())/float64(b.N), "ns/p95_latency")
	}
}

// BenchmarkStreamToPost_JSONMarshal benchmarks the JSON marshaling overhead for props.
func BenchmarkStreamToPost_JSONMarshal(b *testing.B) {
	bundle := i18n.Init()

	// Test scenarios that require JSON marshaling
	scenarios := []struct {
		name      string
		generator streamBenchGenerator
	}{
		{
			name: "tool_calls_marshal",
			generator: streamBenchGenerator{
				TotalTextSize:    2000,
				ChunkSize:        100,
				IncludeToolCalls: true,
			},
		},
		{
			name: "annotations_marshal",
			generator: streamBenchGenerator{
				TotalTextSize:      2000,
				ChunkSize:          100,
				IncludeAnnotations: true,
			},
		},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				client := newBenchmarkClient()
				service := NewMMPostStreamService(client, bundle)

				stream := sc.generator.generate()
				post := &model.Post{
					Id:        "bench-post-id",
					ChannelId: "bench-channel-id",
				}

				ctx := context.Background()
				service.StreamToPost(ctx, stream, post, "en")
			}
		})
	}
}

// BenchmarkMMPostStreamService_ContextManagement benchmarks the context management overhead.
func BenchmarkMMPostStreamService_ContextManagement(b *testing.B) {
	bundle := i18n.Init()

	b.Run("get_streaming_context", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			client := newBenchmarkClient()
			service := NewMMPostStreamService(client, bundle)

			postID := "bench-post-" + string(rune(i%1000))
			ctx, err := service.GetStreamingContext(context.Background(), postID)
			if err != nil {
				b.Fatal(err)
			}
			_ = ctx
			service.FinishStreaming(postID)
		}
	})

	b.Run("finish_streaming", func(b *testing.B) {
		client := newBenchmarkClient()
		service := NewMMPostStreamService(client, bundle)

		// Pre-populate contexts
		postIDs := make([]string, b.N)
		for i := 0; i < b.N; i++ {
			postIDs[i] = "bench-post-" + string(rune(i))
			_, _ = service.GetStreamingContext(context.Background(), postIDs[i])
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			service.FinishStreaming(postIDs[i])
		}
	})
}
