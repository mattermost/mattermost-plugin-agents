// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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

// WithBoundParams creates a new Tool with parameters bound to fixed values.
// Bound parameters are:
// - Removed from the schema (LLM cannot see or manipulate them)
// - Automatically injected when the resolver is called
func (t Tool) WithBoundParams(params map[string]interface{}) Tool {
	return Tool{
		Name:        t.Name,
		Description: t.Description,
		Schema:      removeSchemaProperties(t.Schema, params),
		Resolver:    wrapResolverWithBoundParams(t.Resolver, params),
	}
}

// removeSchemaProperties removes the specified properties from a JSON schema.
// It returns a modified copy of the schema, leaving the original unchanged.
func removeSchemaProperties(schema any, params map[string]interface{}) any {
	if schema == nil || len(params) == 0 {
		return schema
	}

	// Type assert to *jsonschema.Schema
	jsonSchema, ok := schema.(*jsonschema.Schema)
	if !ok {
		// If not a jsonschema.Schema, return as-is
		return schema
	}

	// Create a shallow copy of the schema
	newSchema := *jsonSchema

	// Copy and filter properties
	if jsonSchema.Properties != nil {
		newSchema.Properties = make(map[string]*jsonschema.Schema)
		for name, prop := range jsonSchema.Properties {
			if _, isBound := params[name]; !isBound {
				newSchema.Properties[name] = prop
			}
		}
	}

	// Copy and filter required array
	if len(jsonSchema.Required) > 0 {
		newSchema.Required = make([]string, 0, len(jsonSchema.Required))
		for _, name := range jsonSchema.Required {
			if _, isBound := params[name]; !isBound {
				newSchema.Required = append(newSchema.Required, name)
			}
		}
	}

	return &newSchema
}

// wrapResolverWithBoundParams creates a wrapped resolver that injects bound parameters
func wrapResolverWithBoundParams(original ToolResolver, params map[string]interface{}) ToolResolver {
	if original == nil || len(params) == 0 {
		return original
	}

	return func(context *Context, argsGetter ToolArgumentGetter) (string, error) {
		wrappedGetter := func(args any) error {
			// First unmarshal the original args
			if err := argsGetter(args); err != nil {
				return err
			}
			// Then inject bound params
			return injectBoundParams(args, params)
		}
		return original(context, wrappedGetter)
	}
}

// injectBoundParams injects bound parameter values into the args struct or map
func injectBoundParams(args any, params map[string]interface{}) error {
	if len(params) == 0 {
		return nil
	}

	val := reflect.ValueOf(args)
	if val.Kind() != reflect.Ptr || val.IsNil() {
		return fmt.Errorf("args must be a non-nil pointer, got %T", args)
	}

	elem := val.Elem()

	// Handle map[string]interface{} or similar maps
	if elem.Kind() == reflect.Map {
		if elem.IsNil() {
			elem.Set(reflect.MakeMap(elem.Type()))
		}
		for k, v := range params {
			elem.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v))
		}
		return nil
	}

	// Handle Struct
	if elem.Kind() == reflect.Struct {
		for k, v := range params {
			field := findFieldByNameOrTag(elem, k)
			if field.IsValid() && field.CanSet() {
				valToSet := reflect.ValueOf(v)
				if valToSet.Type().ConvertibleTo(field.Type()) {
					field.Set(valToSet.Convert(field.Type()))
				} else if valToSet.Kind() == reflect.Float64 {
					// Handle JSON number to int conversion
					switch field.Kind() {
					case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
						field.SetInt(int64(valToSet.Float()))
					case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
						field.SetUint(uint64(valToSet.Float()))
					}
				}
			}
		}
		return nil
	}

	return nil
}

// findFieldByNameOrTag finds a struct field by name or json tag
func findFieldByNameOrTag(val reflect.Value, name string) reflect.Value {
	typ := val.Type()

	// First try exact match on field name
	if f := val.FieldByName(name); f.IsValid() {
		return f
	}

	// Try json tag
	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" {
			continue
		}
		// Handle "name,omitempty"
		parts := strings.Split(tag, ",")
		if parts[0] == name {
			return val.Field(i)
		}
	}
	return reflect.Value{}
}

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

// AutoRunResult represents the result of executing an auto-run tool
type AutoRunResult struct {
	ToolCallID string
	ToolName   string
	Result     string
	IsError    bool
}

// ShouldAutoRunTools checks if all pending tool calls are configured for auto-run.
// Returns true only if AutoRunTools is configured and ALL tool calls are in the auto-run list.
func ShouldAutoRunTools(pendingToolCalls []ToolCall, autoRunTools []string) bool {
	if len(autoRunTools) == 0 || len(pendingToolCalls) == 0 {
		return false
	}

	autoRunSet := make(map[string]bool, len(autoRunTools))
	for _, name := range autoRunTools {
		autoRunSet[name] = true
	}

	for _, tc := range pendingToolCalls {
		if !autoRunSet[tc.Name] {
			return false
		}
	}
	return true
}

// ExecuteAutoRunTools executes the given tool calls using the provided resolver.
// Returns the results for each tool call.
func ExecuteAutoRunTools(
	pendingToolCalls []ToolCall,
	resolver func(name string, argsGetter ToolArgumentGetter, context *Context) (string, error),
	context *Context,
) []AutoRunResult {
	results := make([]AutoRunResult, 0, len(pendingToolCalls))

	for _, tc := range pendingToolCalls {
		getter := func(args any) error { return json.Unmarshal(tc.Arguments, args) }

		result, err := resolver(tc.Name, getter, context)
		isError := err != nil
		if err != nil {
			result = fmt.Sprintf("Error executing tool: %v", err)
		}

		results = append(results, AutoRunResult{
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Result:     result,
			IsError:    isError,
		})
	}

	return results
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

// GetTool returns a pointer to a tool by name, or nil if not found
func (s *ToolStore) GetTool(name string) *Tool {
	if tool, ok := s.tools[name]; ok {
		return &tool
	}
	return nil
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
