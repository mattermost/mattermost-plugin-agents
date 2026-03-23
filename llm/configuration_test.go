// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBotConfig_IsValid(t *testing.T) {
	type fields struct {
		ID                 string
		Name               string
		DisplayName        string
		CustomInstructions string
		ServiceID          string
		Service            *ServiceConfig
		EnableVision       bool
		DisableTools       bool
		ChannelAccessLevel ChannelAccessLevel
		ChannelIDs         []string
		UserAccessLevel    UserAccessLevel
		UserIDs            []string
		TeamIDs            []string
		MaxFileSize        int64
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "Valid OpenAI configuration with minimal required fields",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: true,
		},
		{
			name: "Valid OpenAI configuration with ChannelAccessLevelNone",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelNone,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: true,
		},
		{
			name: "Bot name cannot be empty",
			fields: fields{
				ID:                 "xxx",
				Name:               "", // bad
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: false,
		},
		{
			name: "Bot display name cannot be empty",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "", // bad
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: false,
		},
		{
			name: "ServiceID cannot be empty",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "", // bad - empty service ID
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: false,
		},
		{
			name: "Channel access level cannot be less than ChannelAccessLevelAll (0)",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll - 1, // bad
				UserAccessLevel:    UserAccessLevelNone,
			},
			want: false,
		},
		{
			name: "Channel access level cannot be greater than ChannelAccessLevelNone (3)",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelNone + 1, // bad
				UserAccessLevel:    UserAccessLevelNone,
			},
			want: false,
		},
		{
			name: "User access level cannot be less than UserAccessLevelAll (0)",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll - 1, // bad
			},
			want: false,
		},
		{
			name: "User access level cannot be greater than UserAccessLevelNone (3)",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelNone + 1, // bad
			},
			want: false,
		},
		{
			name: "Bot with valid ServiceID should pass",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: true,
		},
		{
			name: "Bot with valid ServiceID should pass (second case)",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				ServiceID:          "service-id",
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &BotConfig{
				ID:                 tt.fields.ID,
				Name:               tt.fields.Name,
				DisplayName:        tt.fields.DisplayName,
				CustomInstructions: tt.fields.CustomInstructions,
				ServiceID:          tt.fields.ServiceID,
				Service:            tt.fields.Service,
				EnableVision:       tt.fields.EnableVision,
				DisableTools:       tt.fields.DisableTools,
				ChannelAccessLevel: tt.fields.ChannelAccessLevel,
				ChannelIDs:         tt.fields.ChannelIDs,
				UserAccessLevel:    tt.fields.UserAccessLevel,
				UserIDs:            tt.fields.UserIDs,
				TeamIDs:            tt.fields.TeamIDs,
				MaxFileSize:        tt.fields.MaxFileSize,
			}
			assert.Equalf(t, tt.want, c.IsValid(), "IsValid() for test case %q", tt.name)
		})
	}
}

func TestIsValidService(t *testing.T) {
	tests := []struct {
		name    string
		service ServiceConfig
		want    bool
	}{
		{
			name: "valid openai service",
			service: ServiceConfig{
				ID:     "service-1",
				Type:   ServiceTypeOpenAI,
				APIKey: "sk-xyz",
			},
			want: true,
		},
		{
			name: "valid openai compatible service with api url",
			service: ServiceConfig{
				ID:     "service-2",
				Type:   ServiceTypeOpenAICompatible,
				APIURL: "http://localhost:11434/v1",
			},
			want: true,
		},
		{
			name: "valid dynamically discovered provider with no extra fields",
			service: ServiceConfig{
				ID:   "service-3",
				Type: "vertex",
			},
			want: true,
		},
		{
			name: "valid service with advanced bifrost key config",
			service: ServiceConfig{
				ID:             "service-4",
				Type:           "vllm",
				BifrostKeyJSON: `{"vllm_key_config":{"url":"http://vllm:8000","model_name":"llama-3.1-8b"}}`,
			},
			want: true,
		},
		{
			name: "valid service with advanced bifrost provider config",
			service: ServiceConfig{
				ID:                        "service-5",
				Type:                      "ollama",
				BifrostProviderConfigJSON: `{"network_config":{"base_url":"http://ollama:11434","default_request_timeout_in_seconds":120}}`,
			},
			want: true,
		},
		{
			name: "service with malformed bifrost key json",
			service: ServiceConfig{
				ID:             "service-6",
				Type:           "vertex",
				BifrostKeyJSON: `{"vertex_key_config":`,
			},
			want: false,
		},
		{
			name: "service with malformed bifrost provider config json",
			service: ServiceConfig{
				ID:                        "service-7",
				Type:                      "openai",
				BifrostProviderConfigJSON: `{"network_config":`,
			},
			want: false,
		},
		{
			name: "service with empty id",
			service: ServiceConfig{
				ID:     "",
				Type:   ServiceTypeOpenAI,
				APIKey: "sk-xyz",
			},
			want: false,
		},
		{
			name: "service with empty type",
			service: ServiceConfig{
				ID:     "service-8",
				Type:   "",
				APIKey: "sk-xyz",
			},
			want: false,
		},
		{
			name: "service with unsupported type",
			service: ServiceConfig{
				ID:     "service-9",
				Type:   "mattermostllm",
				APIKey: "sk-xyz",
			},
			want: false,
		},
		{
			name: "legacy scale service is no longer valid",
			service: ServiceConfig{
				ID:     "service-10",
				Type:   "scale",
				APIKey: "scale-key",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidService(tt.service)
			assert.Equalf(t, tt.want, result, "IsValidService() for test case %q", tt.name)
		})
	}
}

func TestServiceConfig_JSONUnmarshal_sendUserID(t *testing.T) {
	const payload = `{"id":"s1","name":"x","type":"openai","sendUserID":true}`
	var cfg ServiceConfig
	err := json.Unmarshal([]byte(payload), &cfg)
	require.NoError(t, err)
	assert.True(t, cfg.SendUserID)
}
