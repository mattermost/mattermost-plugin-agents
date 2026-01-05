// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"
)

// BenchmarkReadAll benchmarks the ReadAll() function with varying response sizes.
// This measures the overhead of string concatenation and event processing.
func BenchmarkReadAll(b *testing.B) {
	scenarios := BenchmarkScenarios()

	for _, sc := range scenarios {
		// Skip tool_calls scenario since ReadAll returns error for tool calls
		if sc.Name == "with_tool_calls" {
			continue
		}

		b.Run(sc.Name, func(b *testing.B) {
			for b.Loop() {
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

// BenchmarkStreamConsumption_RawChannel measures raw channel read speed.
// This provides a baseline for channel overhead without any processing.
func BenchmarkStreamConsumption_RawChannel(b *testing.B) {
	scenarios := BenchmarkScenarios()

	for _, sc := range scenarios {
		b.Run(sc.Name, func(b *testing.B) {
			for b.Loop() {
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
