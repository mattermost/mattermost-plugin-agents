// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tool represents a function that can be called by the language model during a conversation.
//
// Each tool has a name, description, and schema that defines its parameters. These are passed to the LLM for it to understand what capabilities it has.
// It is the Resolver function that implements the actual functionality.
//
// The Schema field should contain a JSONSchema that defines the expected structure of the tool's arguments.
// The Resolver function receives the conversation context and a way to access the parsed arguments,
// and returns either a result that will be passed to the LLM or an error.
type Tool struct {
	Name        string
	Description string
	Schema      any
	Resolver    ToolResolver
}

type ToolResolver func(context *Context, argsGetter ToolArgumentGetter) (string, error)

// ToolCallStatus represents the current status of a tool call
type ToolCallStatus int

const (
	// ToolCallStatusPending indicates the tool is waiting for user approval/rejection
	ToolCallStatusPending ToolCallStatus = iota
	// ToolCallStatusAccepted indicates the user has accepted the tool call but it's not resolved yet
	ToolCallStatusAccepted
	// ToolCallStatusRejected indicates the user has rejected the tool call
	ToolCallStatusRejected
	// ToolCallStatusError indicates the tool call was accepted but errored during resolution
	ToolCallStatusError
	// ToolCallStatusSuccess indicates the tool call was accepted and resolved successfully
	ToolCallStatusSuccess
)

// ToolCall represents a tool call. An empty result indicates that the tool has not yet been resolved.
type ToolCall struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Arguments   json.RawMessage `json:"arguments"`
	Result      string          `json:"result"`
	Status      ToolCallStatus  `json:"status"`
}

// SanitizeNonPrintableChars replaces non-printable Unicode characters with their
// escaped representation [U+XXXX] to prevent text spoofing attacks such as
// bidirectional text attacks that can make URLs appear to point to different domains.
// Uses [U+XXXX] format instead of \uXXXX to avoid JSON parsers converting it back.
// Allows newline, tab, and carriage return for JSON formatting.
// Also escapes variation selectors and other default ignorable code points which
// are technically "printable" but render invisibly and can be used for spoofing.
func SanitizeNonPrintableChars(s string) string {
	// Quick scan: check if any character needs escaping.
	// This avoids allocation for the common case of clean strings.
	needsEscape := false
	for _, r := range s {
		if !isSafeRune(r) {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return s
	}

	var result strings.Builder
	result.Grow(len(s))

	for _, r := range s {
		if isSafeRune(r) {
			result.WriteRune(r)
		} else {
			fmt.Fprintf(&result, "[U+%04X]", r)
		}
	}
	return result.String()
}

// isSafeRune reports whether a rune can pass through without escaping.
func isSafeRune(r rune) bool {
	// Fast path: printable ASCII characters (space through tilde)
	if r >= 0x20 && r <= 0x7E {
		return true
	}
	// Allow formatting control characters needed for JSON
	if r == '\n' || r == '\t' || r == '\r' {
		return true
	}
	// For non-ASCII, check if printable and not in a problematic category
	if r > 0x7E {
		return unicode.IsPrint(r) &&
			!unicode.Is(unicode.Variation_Selector, r) &&
			!unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r)
	}
	return false
}

// SanitizeArguments sanitizes the Arguments field to prevent
// bidirectional text and other Unicode spoofing attacks.
func (tc *ToolCall) SanitizeArguments() {
	if len(tc.Arguments) > 0 {
		tc.Arguments = json.RawMessage(SanitizeNonPrintableChars(string(tc.Arguments)))
	}
}

type ToolArgumentGetter func(args any) error

// ToolAuthError represents an authentication error that occurred during tool creation
type ToolAuthError struct {
	ServerName string `json:"server_name"`
	AuthURL    string `json:"auth_url"`
	Error      error  `json:"error"`
}

type ToolStore struct {
	tools      map[string]Tool
	log        TraceLog
	doTrace    bool
	authErrors []ToolAuthError
}

type TraceLog interface {
	Info(message string, keyValuePairs ...any)
}

// NewJSONSchemaFromStruct creates a JSONSchema from a Go struct using generics
// It's a helper function for tool providers that currently define schemas as structs
func NewJSONSchemaFromStruct[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create JSON schema from struct: %v", err))
	}

	return schema
}

func NewNoTools() *ToolStore {
	return &ToolStore{
		tools:      make(map[string]Tool),
		log:        nil,
		doTrace:    false,
		authErrors: []ToolAuthError{},
	}
}

func NewToolStore(log TraceLog, doTrace bool) *ToolStore {
	return &ToolStore{
		tools:      make(map[string]Tool),
		log:        log,
		doTrace:    doTrace,
		authErrors: []ToolAuthError{},
	}
}

func (s *ToolStore) AddTools(tools []Tool) {
	for _, tool := range tools {
		s.tools[tool.Name] = tool
	}
}

func (s *ToolStore) ResolveTool(name string, argsGetter ToolArgumentGetter, context *Context) (string, error) {
	tool, ok := s.tools[name]
	if !ok {
		s.TraceUnknown(name, argsGetter)
		return "", errors.New("unknown tool " + name)
	}
	results, err := tool.Resolver(context, argsGetter)
	s.TraceResolved(name, argsGetter, results, err)
	return results, err
}

func (s *ToolStore) GetTools() []Tool {
	result := make([]Tool, 0, len(s.tools))
	for _, tool := range s.tools {
		result = append(result, tool)
	}
	return result
}

// GetToolsInfo returns basic information (name and description) about all tools in the store.
// This is useful for informing LLMs about tools that are available in other contexts
// (e.g., DM-only tools when in a channel).
func (s *ToolStore) GetToolsInfo() []ToolInfo {
	if s == nil || len(s.tools) == 0 {
		return nil
	}
	result := make([]ToolInfo, 0, len(s.tools))
	for _, tool := range s.tools {
		result = append(result, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
		})
	}
	return result
}

func (s *ToolStore) TraceUnknown(name string, argsGetter ToolArgumentGetter) {
	if s.log != nil && s.doTrace {
		args := ""
		var raw json.RawMessage
		if err := argsGetter(&raw); err != nil {
			args = fmt.Sprintf("failed to get tool args: %v", err)
		} else {
			args = string(raw)
		}
		s.log.Info("unknown tool called", "name", name, "args", args)
	}
}

func (s *ToolStore) TraceResolved(name string, argsGetter ToolArgumentGetter, result string, err error) {
	if s.log != nil && s.doTrace {
		args := ""
		var raw json.RawMessage
		if getArgsErr := argsGetter(&raw); getArgsErr != nil {
			args = fmt.Sprintf("failed to get tool args: %v", getArgsErr)
		} else {
			args = string(raw)
		}
		s.log.Info("tool resolved", "name", name, "args", args, "result", result, "error", err)
	}
}

// AddAuthError adds an authentication error to the tool store
func (s *ToolStore) AddAuthError(authError ToolAuthError) {
	s.authErrors = append(s.authErrors, authError)
}

// GetAuthErrors returns all authentication errors collected during tool creation
func (s *ToolStore) GetAuthErrors() []ToolAuthError {
	return s.authErrors
}
