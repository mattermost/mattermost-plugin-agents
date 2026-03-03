// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"testing"
)

func TestEnableTokenUsageSinks(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *Config
		pluginEnv  *string
		fileEnv    *string
		wantPlugin bool
		wantFile   bool
	}{
		{
			name:       "nil config",
			cfg:        nil,
			wantPlugin: false,
			wantFile:   false,
		},
		{
			name: "token usage logging disabled overrides env settings",
			cfg: &Config{
				EnableTokenUsageLogging:     false,
				EnableTokenUsageLogToPlugin: boolPtr(true),
				EnableTokenUsageLogToFile:   boolPtr(true),
			},
			pluginEnv:  stringPtr("true"),
			fileEnv:    stringPtr("true"),
			wantPlugin: false,
			wantFile:   false,
		},
		{
			name: "legacy defaults apply when env vars are not set",
			cfg: &Config{
				EnableTokenUsageLogging:     true,
				EnableTokenUsageLogToPlugin: boolPtr(true),
				EnableTokenUsageLogToFile:   boolPtr(false),
			},
			wantPlugin: false,
			wantFile:   true,
		},
		{
			name: "only plugin env var set",
			cfg: &Config{
				EnableTokenUsageLogging:     true,
				EnableTokenUsageLogToPlugin: boolPtr(false),
				EnableTokenUsageLogToFile:   boolPtr(false),
			},
			pluginEnv:  stringPtr("true"),
			wantPlugin: true,
			wantFile:   true,
		},
		{
			name: "only file env var set",
			cfg: &Config{
				EnableTokenUsageLogging:     true,
				EnableTokenUsageLogToPlugin: boolPtr(true),
				EnableTokenUsageLogToFile:   boolPtr(false),
			},
			fileEnv:    stringPtr("false"),
			wantPlugin: false,
			wantFile:   false,
		},
		{
			name: "both env vars set",
			cfg: &Config{
				EnableTokenUsageLogging:     true,
				EnableTokenUsageLogToPlugin: nil,
				EnableTokenUsageLogToFile:   boolPtr(true),
			},
			pluginEnv:  stringPtr("true"),
			fileEnv:    stringPtr("false"),
			wantPlugin: true,
			wantFile:   false,
		},
		{
			name: "invalid env var values fall back to legacy defaults",
			cfg: &Config{
				EnableTokenUsageLogging: true,
			},
			pluginEnv:  stringPtr("notabool"),
			fileEnv:    stringPtr("notabool"),
			wantPlugin: false,
			wantFile:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env vars by default so tests do not depend on host environment.
			t.Setenv(tokenUsageLogToPluginEnvKey, "")
			t.Setenv(tokenUsageLogToFileEnvKey, "")
			if tt.pluginEnv != nil {
				t.Setenv(tokenUsageLogToPluginEnvKey, *tt.pluginEnv)
			}
			if tt.fileEnv != nil {
				t.Setenv(tokenUsageLogToFileEnvKey, *tt.fileEnv)
			}

			container := &Container{}
			container.Update(tt.cfg)

			if got := container.EnableTokenUsageLogToPlugin(); got != tt.wantPlugin {
				t.Fatalf("EnableTokenUsageLogToPlugin() = %t, want %t", got, tt.wantPlugin)
			}

			if got := container.EnableTokenUsageLogToFile(); got != tt.wantFile {
				t.Fatalf("EnableTokenUsageLogToFile() = %t, want %t", got, tt.wantFile)
			}
		})
	}
}

func TestTokenUsageSinkConfigUnmarshalCompatibility(t *testing.T) {
	tests := []struct {
		name                string
		payload             string
		wantPluginNil       bool
		wantPluginValue     bool
		wantFileNil         bool
		wantFileValue       bool
		wantPluginEnabledBy bool
		wantFileEnabledBy   bool
	}{
		{
			name:                "legacy payload keeps sink pointers nil",
			payload:             `{"enableTokenUsageLogging":true}`,
			wantPluginNil:       true,
			wantFileNil:         true,
			wantPluginEnabledBy: false,
			wantFileEnabledBy:   true,
		},
		{
			name:                "explicit false values are preserved",
			payload:             `{"enableTokenUsageLogging":true,"enableTokenUsageLogToPlugin":false,"enableTokenUsageLogToFile":false}`,
			wantPluginNil:       false,
			wantPluginValue:     false,
			wantFileNil:         false,
			wantFileValue:       false,
			wantPluginEnabledBy: false,
			wantFileEnabledBy:   true,
		},
		{
			name:                "explicit true plugin value is preserved",
			payload:             `{"enableTokenUsageLogging":true,"enableTokenUsageLogToPlugin":true}`,
			wantPluginNil:       false,
			wantPluginValue:     true,
			wantFileNil:         true,
			wantPluginEnabledBy: false,
			wantFileEnabledBy:   true,
		},
		{
			name:                "explicit true file value is preserved",
			payload:             `{"enableTokenUsageLogging":true,"enableTokenUsageLogToFile":true}`,
			wantPluginNil:       true,
			wantFileNil:         false,
			wantFileValue:       true,
			wantPluginEnabledBy: false,
			wantFileEnabledBy:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Keep env-based accessor behavior deterministic for compatibility checks.
			t.Setenv(tokenUsageLogToPluginEnvKey, "")
			t.Setenv(tokenUsageLogToFileEnvKey, "")

			var cfg Config
			if err := json.Unmarshal([]byte(tt.payload), &cfg); err != nil {
				t.Fatalf("failed to unmarshal config payload: %v", err)
			}

			if got := cfg.EnableTokenUsageLogToPlugin == nil; got != tt.wantPluginNil {
				t.Fatalf("plugin sink nil = %t, want %t", got, tt.wantPluginNil)
			}
			if !tt.wantPluginNil && *cfg.EnableTokenUsageLogToPlugin != tt.wantPluginValue {
				t.Fatalf("plugin sink value = %t, want %t", *cfg.EnableTokenUsageLogToPlugin, tt.wantPluginValue)
			}

			if got := cfg.EnableTokenUsageLogToFile == nil; got != tt.wantFileNil {
				t.Fatalf("file sink nil = %t, want %t", got, tt.wantFileNil)
			}
			if !tt.wantFileNil && *cfg.EnableTokenUsageLogToFile != tt.wantFileValue {
				t.Fatalf("file sink value = %t, want %t", *cfg.EnableTokenUsageLogToFile, tt.wantFileValue)
			}

			container := &Container{}
			container.Update(&cfg)
			if got := container.EnableTokenUsageLogToPlugin(); got != tt.wantPluginEnabledBy {
				t.Fatalf("EnableTokenUsageLogToPlugin() = %t, want %t", got, tt.wantPluginEnabledBy)
			}
			if got := container.EnableTokenUsageLogToFile(); got != tt.wantFileEnabledBy {
				t.Fatalf("EnableTokenUsageLogToFile() = %t, want %t", got, tt.wantFileEnabledBy)
			}
		})
	}
}

func TestTokenUsageSinkConfigMarshal(t *testing.T) {
	tests := []struct {
		name              string
		cfg               Config
		expectPluginField bool
		expectFileField   bool
	}{
		{
			name: "unset sink pointers are omitted",
			cfg: Config{
				EnableTokenUsageLogging: true,
			},
			expectPluginField: false,
			expectFileField:   false,
		},
		{
			name: "explicit sink values are serialized",
			cfg: Config{
				EnableTokenUsageLogging:     true,
				EnableTokenUsageLogToPlugin: boolPtr(false),
				EnableTokenUsageLogToFile:   boolPtr(true),
			},
			expectPluginField: true,
			expectFileField:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.cfg)
			if err != nil {
				t.Fatalf("failed to marshal config payload: %v", err)
			}

			var parsed map[string]any
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatalf("failed to parse marshaled config payload: %v", err)
			}

			_, hasPluginField := parsed["enableTokenUsageLogToPlugin"]
			if hasPluginField != tt.expectPluginField {
				t.Fatalf("plugin sink field present = %t, want %t", hasPluginField, tt.expectPluginField)
			}

			_, hasFileField := parsed["enableTokenUsageLogToFile"]
			if hasFileField != tt.expectFileField {
				t.Fatalf("file sink field present = %t, want %t", hasFileField, tt.expectFileField)
			}
		})
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func stringPtr(value string) *string {
	return &value
}
