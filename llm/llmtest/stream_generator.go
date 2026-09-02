// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmtest

import (
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// StreamGenerator creates synthetic streams for benchmarking.
// It generates TextStreamResult with configurable event patterns.
type StreamGenerator struct {
	// TotalTextSize is the total bytes of text to generate
	TotalTextSize int
	// ChunkSize is the size of each text chunk
	ChunkSize int
	// IncludeReasoning adds reasoning events before text events
	IncludeReasoning bool
	// IncludeToolCalls ends with tool calls instead of normal end event
	IncludeToolCalls bool
	// IncludeUsage adds a usage event before end
	IncludeUsage bool
	// IncludeAnnotations adds annotation events
	IncludeAnnotations bool
}

// Generate creates a new TextStreamResult with synthetic events.
// The stream is generated in a goroutine and returned immediately.
func (g *StreamGenerator) Generate() *llm.TextStreamResult {
	bufferSize := (g.TotalTextSize / max(g.ChunkSize, 1)) + 10
	stream := make(chan llm.TextStreamEvent, bufferSize)

	go func() {
		defer close(stream)

		// Generate reasoning events first if enabled
		if g.IncludeReasoning {
			reasoningText := GenerateBenchText(g.TotalTextSize / 2)
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
		text := GenerateBenchText(g.TotalTextSize)
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
					{
						Type:      llm.AnnotationTypeURLCitation,
						URL:       "https://example.com/source2",
						Title:     "Example Source 2",
						CitedText: "More cited text",
						Index:     2,
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

// GenerateBenchText creates a string of the specified size using a repeating pattern.
// Uses a realistic text pattern rather than random bytes.
func GenerateBenchText(size int) string {
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

// BenchmarkScenario defines a benchmark test scenario
type BenchmarkScenario struct {
	Name      string
	Generator StreamGenerator
}

// BenchmarkScenarios returns common scenarios for stream benchmarks.
// This combines size-based scenarios with event-type scenarios.
func BenchmarkScenarios() []BenchmarkScenario {
	return []BenchmarkScenario{
		// Size-based scenarios
		{
			Name: "small_100_tokens",
			Generator: StreamGenerator{
				TotalTextSize: 400,
				ChunkSize:     40,
			},
		},
		{
			Name: "medium_1k_tokens",
			Generator: StreamGenerator{
				TotalTextSize: 4000,
				ChunkSize:     100,
			},
		},
		{
			Name: "large_8k_tokens",
			Generator: StreamGenerator{
				TotalTextSize: 32000,
				ChunkSize:     200,
			},
		},
		{
			Name: "xlarge_32k_tokens",
			Generator: StreamGenerator{
				TotalTextSize: 128000,
				ChunkSize:     500,
			},
		},
		// Event-type scenarios
		{
			Name: "with_reasoning",
			Generator: StreamGenerator{
				TotalTextSize:    4000,
				ChunkSize:        100,
				IncludeReasoning: true,
			},
		},
		{
			Name: "with_tool_calls",
			Generator: StreamGenerator{
				TotalTextSize:    4000,
				ChunkSize:        100,
				IncludeToolCalls: true,
			},
		},
		{
			Name: "with_usage",
			Generator: StreamGenerator{
				TotalTextSize: 4000,
				ChunkSize:     100,
				IncludeUsage:  true,
			},
		},
		{
			Name: "with_annotations",
			Generator: StreamGenerator{
				TotalTextSize:      4000,
				ChunkSize:          100,
				IncludeAnnotations: true,
			},
		},
		{
			Name: "full_realistic",
			Generator: StreamGenerator{
				TotalTextSize:      8000,
				ChunkSize:          150,
				IncludeReasoning:   true,
				IncludeUsage:       true,
				IncludeAnnotations: true,
			},
		},
	}
}
