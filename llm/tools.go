// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

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

type ToolArgumentGetter func(args any) error

// MergeArguments creates a new ToolArgumentGetter that merges overrides into the original arguments.
func MergeArguments(originalGetter ToolArgumentGetter, overrides map[string]interface{}) ToolArgumentGetter {
	return func(args any) error {
		if err := originalGetter(args); err != nil {
			return err
		}

		if len(overrides) == 0 {
			return nil
		}

		val := reflect.ValueOf(args)
		if val.Kind() != reflect.Ptr || val.IsNil() {
			return fmt.Errorf("args must be a non-nil pointer")
		}

		elem := val.Elem()

		// Handle map[string]interface{} or similar maps
		if elem.Kind() == reflect.Map {
			if elem.IsNil() {
				// Should have been initialized by originalGetter usually, but if not:
				elem.Set(reflect.MakeMap(elem.Type()))
			}
			for k, v := range overrides {
				// We need to ensure the value type is compatible, but for map[string]interface{} it's fine.
				elem.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v))
			}
			return nil
		}

		// Handle Struct
		if elem.Kind() == reflect.Struct {
			for k, v := range overrides {
				// Try to find field by name or json tag
				field := findField(elem, k)
				if field.IsValid() && field.CanSet() {
					valToSet := reflect.ValueOf(v)
					// Simple type conversion
					if valToSet.Type().ConvertibleTo(field.Type()) {
						field.Set(valToSet.Convert(field.Type()))
					} else {
						// Fallback for common JSON number cases (float64 to int)
						if valToSet.Kind() == reflect.Float64 {
							switch field.Kind() {
							case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
								field.SetInt(int64(valToSet.Float()))
							case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
								field.SetUint(uint64(valToSet.Float()))
							}
						}
					}
				}
			}
			return nil
		}

		return nil
	}
}

func findField(val reflect.Value, name string) reflect.Value {
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
