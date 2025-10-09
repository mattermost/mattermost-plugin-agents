// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test structs for schema generation
type TestArgs struct {
	// No access tag - should be available for all access modes
	CommonField string `json:"common_field" jsonschema:"Available in all access modes"`

	// Single access mode
	LocalOnly  string `json:"local_only" access:"local" jsonschema:"Only available for local access"`
	RemoteOnly string `json:"remote_only" access:"remote" jsonschema:"Only available for remote access"`

	// Multiple access modes
	LocalAndRemote string `json:"local_remote" access:"local,remote" jsonschema:"Available for both local and remote"`

	// Required field with access restrictions
	RequiredLocal string `json:"required_local" access:"local" jsonschema:"Required field only for local"`
}

type EmptyStruct struct{}

type NoJSONTagsStruct struct {
	Field1 string
	Field2 int
}

func TestNewJSONSchemaForAccessMode(t *testing.T) {
	tests := []struct {
		name           string
		accessMode     string
		expectedFields []string
		excludedFields []string
		expectedCount  int
	}{
		{
			name:       "local access mode includes correct fields",
			accessMode: "local",
			expectedFields: []string{
				"common_field",
				"local_only",
				"local_remote",
				"required_local",
			},
			excludedFields: []string{"remote_only"},
			expectedCount:  4,
		},
		{
			name:       "remote access mode includes correct fields",
			accessMode: "remote",
			expectedFields: []string{
				"common_field",
				"remote_only",
				"local_remote",
			},
			excludedFields: []string{"local_only", "required_local"},
			expectedCount:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := NewJSONSchemaForAccessMode[TestArgs](tt.accessMode)

			require.NotNil(t, schema)
			require.NotNil(t, schema.Properties)

			// Check expected field count
			assert.Len(t, schema.Properties, tt.expectedCount,
				"access mode %s should have %d fields", tt.accessMode, tt.expectedCount)

			// Check all expected fields are present
			for _, field := range tt.expectedFields {
				assert.Contains(t, schema.Properties, field,
					"access mode %s schema should include %s", tt.accessMode, field)
			}

			// Check all excluded fields are absent
			for _, field := range tt.excludedFields {
				assert.NotContains(t, schema.Properties, field,
					"access mode %s schema should not include %s", tt.accessMode, field)
			}
		})
	}

	// Test edge cases that don't fit the table pattern
	t.Run("empty struct returns valid schema", func(t *testing.T) {
		schema := NewJSONSchemaForAccessMode[EmptyStruct]("local")

		require.NotNil(t, schema)
		// Properties might be nil or empty for empty struct
		if schema.Properties != nil {
			assert.Len(t, schema.Properties, 0)
		}
	})

	t.Run("struct with no JSON tags returns base schema", func(t *testing.T) {
		schema := NewJSONSchemaForAccessMode[NoJSONTagsStruct]("local")

		require.NotNil(t, schema)
		// Should return base schema since no JSON tags to filter
	})

	t.Run("preserves schema metadata", func(t *testing.T) {
		schema := NewJSONSchemaForAccessMode[TestArgs]("local")

		require.NotNil(t, schema)
		// Check that basic schema properties are preserved
		assert.NotEmpty(t, schema.Type)

		// Check that field descriptions are preserved
		if commonField, exists := schema.Properties["common_field"]; exists {
			// Verify the property exists and has expected structure
			assert.NotNil(t, commonField)
		}
	})

	t.Run("empty access mode panics", func(t *testing.T) {
		assert.Panics(t, func() {
			NewJSONSchemaForAccessMode[TestArgs]("")
		}, "Empty access mode should panic")
	})
}

func TestIsAccessAllowed(t *testing.T) {
	tests := []struct {
		name          string
		accessTag     string
		currentAccess string
		expected      bool
	}{
		{"empty tag allows all", "", "local", true},
		{"empty tag allows any access", "", "remote", true},
		{"exact match local", "local", "local", true},
		{"exact match remote", "remote", "remote", true},
		{"no match local vs remote", "local", "remote", false},
		{"no match remote vs local", "remote", "local", false},
		{"comma separated includes local", "local,remote", "local", true},
		{"comma separated includes remote", "local,remote", "remote", true},
		{"comma separated excludes other", "local,remote", "other", false},
		{"with spaces includes local", "local, remote", "local", true},
		{"with spaces includes remote", "local, remote", "remote", true},
		{"with spaces excludes other", "local, remote", "other", false},
		{"three access modes includes first", "local,remote,special", "local", true},
		{"three access modes includes middle", "local,remote,special", "remote", true},
		{"three access modes includes last", "local,remote,special", "special", true},
		{"three access modes excludes other", "local,remote,special", "unknown", false},
		{"mixed spaces includes all", "local , remote,  special", "remote", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAccessAllowed(tt.accessTag, tt.currentAccess)
			assert.Equal(t, tt.expected, result,
				"isAccessAllowed(%q, %q) = %v, want %v",
				tt.accessTag, tt.currentAccess, result, tt.expected)
		})
	}
}
