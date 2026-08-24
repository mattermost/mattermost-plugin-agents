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
	"context"

	"github.com/google/jsonschema-go/jsonschema"
)

type LanguageModel interface {
	ChatCompletion(ctx context.Context, conversation CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error)
	ChatCompletionNoStream(ctx context.Context, conversation CompletionRequest, opts ...LanguageModelOption) (string, error)

	// CountTokens returns the exact input-token count. Implementations that
	// can't reach a provider counting endpoint return ErrUnsupportedTokenCount
	// so callers can fall back to EstimateTokens.
	CountTokens(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (int, error)

	InputTokenLimit() int
	OutputTokenLimit() int
}

type LanguageModelConfig struct {
	Model                  string
	MaxGeneratedTokens     int
	EnableVision           bool
	JSONOutputFormat       *jsonschema.Schema
	ToolsDisabled          bool
	NativeWebSearchAllowed bool // Allows native web search even when ToolsDisabled is true
	ReasoningDisabled      bool
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

func WithNativeWebSearchAllowed() LanguageModelOption {
	return func(cfg *LanguageModelConfig) {
		cfg.NativeWebSearchAllowed = true
	}
}

func WithReasoningDisabled() LanguageModelOption {
	return func(cfg *LanguageModelConfig) {
		cfg.ReasoningDisabled = true
	}
}

type LanguageModelWrapper func(LanguageModel) LanguageModel

// ProviderFileReference identifies a provider-side file and the provider route
// that created it. ProviderRoute is opaque outside the provider implementation;
// callers must preserve it exactly and must not expose it to users.
type ProviderFileReference struct {
	ID            string
	ProviderRoute string
}

// ProviderFile is a provider-side file's content and metadata.
type ProviderFile struct {
	// Name is the file name the provider recorded. For code-execution output
	// files this is the name the sandbox command wrote, so treat it as
	// model-influenced input and sanitize before use.
	Name        string
	ContentType string
	Content     []byte
}

// ProviderFileDownloader serves provider-side files (e.g. Anthropic Files API
// content for code-execution output files).
//
// Callers reach an implementation through ProviderServices.FileDownloader,
// which is resolved from the concrete provider client at construction time. Do
// not type-assert for this interface on a bot's LanguageModel: that value is a
// decorator chain and the assertion always fails.
type ProviderFileDownloader interface {
	// DownloadProviderFile returns the file's content and metadata. The
	// reference must be the one captured from the provider response so a file
	// produced by a fallback is downloaded with that fallback's route.
	DownloadProviderFile(ctx context.Context, ref ProviderFileReference) (ProviderFile, error)
}
