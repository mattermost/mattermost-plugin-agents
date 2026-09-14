// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/maximhq/bifrost/core/schemas"
)

func buildResponsesJSONSchema(schemaMap map[string]any) (*schemas.ResponsesTextConfigFormatJSONSchema, error) {
	responseSchema := &schemas.ResponsesTextConfigFormatJSONSchema{}

	if typeVal, ok := schemaMap["type"].(string); ok {
		responseSchema.Type = new(typeVal)
	} else if typeList, ok := schemaMap["type"].([]any); ok {
		anyOf := make([]schemas.OrderedMap, 0, len(typeList))
		for i, item := range typeList {
			typeName, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("responses JSON schema type[%d] must be a string", i)
			}
			anyOf = append(anyOf, *schemas.NewOrderedMapFromPairs(schemas.KV("type", typeName)))
		}
		if len(anyOf) > 0 {
			responseSchema.AnyOf = anyOf
		}
	}
	if properties, ok := schemaMap["properties"].(map[string]any); ok {
		responseSchema.Properties = schemas.OrderedMapFromMap(properties)
	}
	if required := extractStringSlice(schemaMap["required"]); len(required) > 0 {
		responseSchema.Required = required
	}
	if description, ok := schemaMap["description"].(string); ok {
		responseSchema.Description = new(description)
	}
	if additionalProps, ok := schemaMap["additionalProperties"].(bool); ok {
		responseSchema.AdditionalProperties = &schemas.AdditionalPropertiesStruct{
			AdditionalPropertiesBool: &additionalProps,
		}
	} else if additionalProps, ok := schemas.SafeExtractOrderedMap(schemaMap["additionalProperties"]); ok {
		responseSchema.AdditionalProperties = &schemas.AdditionalPropertiesStruct{
			AdditionalPropertiesMap: additionalProps,
		}
	}
	if name, ok := schemaMap["name"].(string); ok {
		responseSchema.Name = new(name)
	} else if title, ok := schemaMap["title"].(string); ok {
		responseSchema.Name = new(title)
	}
	if defs, ok := schemaMap["$defs"].(map[string]any); ok {
		responseSchema.Defs = schemas.OrderedMapFromMap(defs)
	}
	if definitions, ok := schemaMap["definitions"].(map[string]any); ok {
		responseSchema.Definitions = schemas.OrderedMapFromMap(definitions)
	}
	if ref, ok := schemaMap["$ref"].(string); ok {
		responseSchema.Ref = new(ref)
	}
	if items, ok := schemaMap["items"].(map[string]any); ok {
		responseSchema.Items = schemas.OrderedMapFromMap(items)
	}
	if minItems, ok := toInt64(schemaMap["minItems"]); ok {
		responseSchema.MinItems = &minItems
	}
	if maxItems, ok := toInt64(schemaMap["maxItems"]); ok {
		responseSchema.MaxItems = &maxItems
	}
	if anyOf := extractSchemaList(schemaMap["anyOf"]); len(anyOf) > 0 {
		responseSchema.AnyOf = append(responseSchema.AnyOf, anyOf...)
	}
	if oneOf := extractSchemaList(schemaMap["oneOf"]); len(oneOf) > 0 {
		responseSchema.OneOf = oneOf
	}
	if allOf := extractSchemaList(schemaMap["allOf"]); len(allOf) > 0 {
		responseSchema.AllOf = allOf
	}
	if format, ok := schemaMap["format"].(string); ok {
		responseSchema.Format = new(format)
	}
	if pattern, ok := schemaMap["pattern"].(string); ok {
		responseSchema.Pattern = new(pattern)
	}
	if minLength, ok := toInt64(schemaMap["minLength"]); ok {
		responseSchema.MinLength = &minLength
	}
	if maxLength, ok := toInt64(schemaMap["maxLength"]); ok {
		responseSchema.MaxLength = &maxLength
	}
	if minimum, ok := toFloat64(schemaMap["minimum"]); ok {
		responseSchema.Minimum = &minimum
	}
	if maximum, ok := toFloat64(schemaMap["maximum"]); ok {
		responseSchema.Maximum = &maximum
	}
	if title, ok := schemaMap["title"].(string); ok {
		responseSchema.Title = new(title)
	}
	if defaultVal, exists := schemaMap["default"]; exists {
		responseSchema.Default = defaultVal
	}
	if nullable, ok := schemaMap["nullable"].(bool); ok {
		responseSchema.Nullable = &nullable
	}

	enumValues, err := extractStringEnum(schemaMap["enum"])
	if err != nil {
		return nil, err
	}
	if len(enumValues) > 0 {
		responseSchema.Enum = enumValues
	}

	return responseSchema, nil
}

func extractStringSlice(value any) []string {
	switch items := value.(type) {
	case []string:
		if len(items) == 0 {
			return nil
		}
		return append([]string(nil), items...)
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			str, ok := item.(string)
			if !ok {
				continue
			}
			result = append(result, str)
		}
		if len(result) == 0 {
			return nil
		}
		return result
	default:
		return nil
	}
}

func extractStringEnum(value any) ([]string, error) {
	switch items := value.(type) {
	case nil:
		return nil, nil
	case []string:
		if len(items) == 0 {
			return nil, nil
		}
		return append([]string(nil), items...), nil
	case []any:
		result := make([]string, 0, len(items))
		for i, item := range items {
			str, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("responses JSON schema enum[%d] must be a string, got %T", i, item)
			}
			result = append(result, str)
		}
		if len(result) == 0 {
			return nil, nil
		}
		return result, nil
	default:
		return nil, fmt.Errorf("responses JSON schema enum must be an array, got %T", value)
	}
}

func extractSchemaList(value any) []schemas.OrderedMap {
	items, ok := value.([]any)
	if !ok {
		return nil
	}

	result := make([]schemas.OrderedMap, 0, len(items))
	for _, item := range items {
		schemaMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, *schemas.OrderedMapFromMap(schemaMap))
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func toInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	default:
		return 0, false
	}
}

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// toolFunctionParams converts a tool schema (a map or any JSON-marshalable
// value) to ToolFunctionParameters, defaulting the type to "object".
func toolFunctionParams(schema any) *schemas.ToolFunctionParameters {
	var params *schemas.ToolFunctionParameters
	if schema != nil {
		switch s := schema.(type) {
		case map[string]any:
			params = schemaMapToFunctionParams(s)
		default:
			// Marshal and unmarshal to convert to map
			data, err := json.Marshal(schema)
			if err == nil {
				var schemaMap map[string]any
				if json.Unmarshal(data, &schemaMap) == nil {
					params = schemaMapToFunctionParams(schemaMap)
				}
			}
		}
	}

	// Ensure params has default values
	if params == nil {
		params = &schemas.ToolFunctionParameters{Type: "object"}
	}
	if params.Type == "" {
		params.Type = "object"
	}
	return params
}

// schemaMapToFunctionParams converts a schema map to ToolFunctionParameters
func schemaMapToFunctionParams(schemaMap map[string]any) *schemas.ToolFunctionParameters {
	params := &schemas.ToolFunctionParameters{
		Type: "object",
	}

	if t, ok := schemaMap["type"].(string); ok {
		params.Type = t
	}
	if desc, ok := schemaMap["description"].(string); ok {
		params.Description = &desc
	}
	if props, ok := schemaMap["properties"].(map[string]any); ok {
		params.Properties = schemas.OrderedMapFromMap(props)
	}
	if req, ok := schemaMap["required"].([]any); ok {
		required := make([]string, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok {
				required = append(required, s)
			}
		}
		params.Required = required
	}

	return params
}

// jsonSchemaToMap converts a *jsonschema.Schema to a map[string]interface{} via JSON round-trip.
func jsonSchemaToMap(schema *jsonschema.Schema) (map[string]any, error) {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON schema: %w", err)
	}
	var schemaMap map[string]any
	if err := json.Unmarshal(data, &schemaMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON schema: %w", err)
	}
	return schemaMap, nil
}

// buildChatResponseFormat creates the response_format parameter for the Chat Completions API.
func buildChatResponseFormat(schema *jsonschema.Schema) *any {
	schemaMap, err := jsonSchemaToMap(schema)
	if err != nil {
		return nil
	}
	var responseFormat any = map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "response",
			"schema": schemaMap,
			"strict": true,
		},
	}
	return &responseFormat
}

// buildResponsesTextConfig creates the text configuration for the Responses API with JSON schema output.
func buildResponsesTextConfig(schema *jsonschema.Schema) (*schemas.ResponsesTextConfig, error) {
	schemaMap, err := jsonSchemaToMap(schema)
	if err != nil {
		return nil, err
	}

	responseSchema, err := buildResponsesJSONSchema(schemaMap)
	if err != nil {
		return nil, err
	}
	return &schemas.ResponsesTextConfig{
		Format: &schemas.ResponsesTextConfigFormat{
			Type:       "json_schema",
			Name:       new("response"),
			Strict:     new(true),
			JSONSchema: responseSchema,
		},
	}, nil
}
