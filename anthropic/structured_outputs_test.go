// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package anthropic

import (
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-ai/llm"
)

func TestConvertJSONSchemaToMap(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]interface{}{
			"name":  map[string]interface{}{"type": "string"},
			"email": map[string]interface{}{"type": "string"},
		},
		Required:             []string{"name", "email"},
		AdditionalProperties: false,
		Description:          "Test schema",
	}

	result := convertJSONSchemaToMap(schema)

	if result["type"] != "object" {
		t.Errorf("Expected type 'object', got %v", result["type"])
	}
	if result["description"] != "Test schema" {
		t.Errorf("Expected description 'Test schema', got %v", result["description"])
	}
	if result["properties"] == nil {
		t.Error("Expected properties to be set")
	}
	if result["required"] == nil {
		t.Error("Expected required to be set")
	}
	if result["additionalProperties"] != false {
		t.Errorf("Expected additionalProperties to be false, got %v", result["additionalProperties"])
	}
}

func TestConvertToolsWithStrictMode(t *testing.T) {
	tools := []llm.Tool{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "string"},
				},
			},
		},
	}

	// Test with strict mode disabled
	resultNoStrict := convertTools(tools, false)
	if len(resultNoStrict) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(resultNoStrict))
	}
	if resultNoStrict[0].OfTool.Strict != nil {
		t.Error("Expected Strict to be nil when strict mode is disabled")
	}

	// Test with strict mode enabled
	resultStrict := convertTools(tools, true)
	if len(resultStrict) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(resultStrict))
	}
	if resultStrict[0].OfTool.Strict == nil || !*resultStrict[0].OfTool.Strict {
		t.Error("Expected Strict to be true when strict mode is enabled")
	}
}
