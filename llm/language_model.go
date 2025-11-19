// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package llm provides a unified abstraction layer for Large Language Model interactions
// within the Mattermost AI plugin.
//
// This package defines the core interfaces and data structures for working with various
// LLM providers (OpenAI, Anthropic, etc.) in a consistent manner. It handles:
//
//   - LanguageModel interface abstraction for different LLM providers
//   - Conversation management with structured posts, roles, and context
//   - Prompt template system with embedded templates and variable substitution
//   - Streaming text responses for real-time chat interactions
//   - Tool/function calling capabilities with JSON schema validation
//   - Request/response structures with token counting and truncation
//   - Context management including user info, channels, and bot configurations
//
// The package is designed to be provider-agnostic, allowing the plugin to work
// with multiple LLM services through a common interface while preserving
// provider-specific capabilities like vision, JSON output, and tool calling.
package llm

import (
	"github.com/google/jsonschema-go/jsonschema"
)

type LanguageModel interface {
	ChatCompletion(conversation CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error)
	ChatCompletionNoStream(conversation CompletionRequest, opts ...LanguageModelOption) (string, error)

	CountTokens(text string) int
	InputTokenLimit() int
}

type LanguageModelConfig struct {
	Model              string
	MaxGeneratedTokens int
	EnableVision       bool
	JSONOutputFormat   *jsonschema.Schema
	ToolsDisabled      bool
}

type LanguageModelOption func(*LanguageModelConfig)

func WithModel(model string) LanguageModelOption {
	return func(cfg *LanguageModelConfig) {
		cfg.Model = model
	}
}
func WithMaxGeneratedTokens(maxGeneratedTokens int) LanguageModelOption {
	return func(cfg *LanguageModelConfig) {
		cfg.MaxGeneratedTokens = maxGeneratedTokens
	}
}
func WithJSONOutput[T any]() LanguageModelOption {
	return func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = NewJSONSchemaFromStruct[T]()
	}
}

func WithToolsDisabled() LanguageModelOption {
	return func(cfg *LanguageModelConfig) {
		cfg.ToolsDisabled = true
	}
}

type LanguageModelWrapper func(LanguageModel) LanguageModel
