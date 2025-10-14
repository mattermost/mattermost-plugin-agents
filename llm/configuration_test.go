// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
		{
			name: "Bot with neither ServiceID nor embedded Service should fail",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				ServiceID:          "",  // no service ID
				Service:            nil, // no embedded service
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: false,
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
			name: "Valid OpenAI service with all required fields",
			service: ServiceConfig{
				ID:     "service-1",
				Type:   ServiceTypeOpenAI,
				APIKey: "sk-xyz",
			},
			want: true,
		},
		{
			name: "Valid OpenAI service with optional fields",
			service: ServiceConfig{
				ID:                      "service-1",
				Name:                    "My OpenAI Service",
				Type:                    ServiceTypeOpenAI,
				APIKey:                  "sk-xyz",
				OrgID:                   "org-xyz",
				DefaultModel:            "gpt-4",
				InputTokenLimit:         100,
				StreamingTimeoutSeconds: 60,
			},
			want: true,
		},
		{
			name: "OpenAI service missing API key",
			service: ServiceConfig{
				ID:     "service-1",
				Type:   ServiceTypeOpenAI,
				APIKey: "", // bad
			},
			want: false,
		},
		{
			name: "Valid OpenAI Compatible service with API URL",
			service: ServiceConfig{
				ID:     "service-2",
				Type:   ServiceTypeOpenAICompatible,
				APIURL: "http://localhost:8080",
			},
			want: true,
		},
		{
			name: "OpenAI Compatible service missing API URL",
			service: ServiceConfig{
				ID:     "service-2",
				Type:   ServiceTypeOpenAICompatible,
				APIURL: "", // bad
			},
			want: false,
		},
		{
			name: "OpenAI Compatible service does not require API key",
			service: ServiceConfig{
				ID:     "service-2",
				Type:   ServiceTypeOpenAICompatible,
				APIKey: "", // not required
				APIURL: "http://localhost:8080",
			},
			want: true,
		},
		{
			name: "Valid Azure service with API key and URL",
			service: ServiceConfig{
				ID:     "service-3",
				Type:   ServiceTypeAzure,
				APIKey: "azure-key",
				APIURL: "https://myservice.openai.azure.com",
			},
			want: true,
		},
		{
			name: "Azure service missing API key",
			service: ServiceConfig{
				ID:     "service-3",
				Type:   ServiceTypeAzure,
				APIKey: "", // bad
				APIURL: "https://myservice.openai.azure.com",
			},
			want: false,
		},
		{
			name: "Azure service missing API URL",
			service: ServiceConfig{
				ID:     "service-3",
				Type:   ServiceTypeAzure,
				APIKey: "azure-key",
				APIURL: "", // bad
			},
			want: false,
		},
		{
			name: "Valid Anthropic service with API key",
			service: ServiceConfig{
				ID:     "service-4",
				Type:   ServiceTypeAnthropic,
				APIKey: "sk-ant-xyz",
			},
			want: true,
		},
		{
			name: "Anthropic service missing API key",
			service: ServiceConfig{
				ID:     "service-4",
				Type:   ServiceTypeAnthropic,
				APIKey: "", // bad
			},
			want: false,
		},
		{
			name: "Valid ASage service with API key",
			service: ServiceConfig{
				ID:     "service-5",
				Type:   ServiceTypeASage,
				APIKey: "asage-key",
			},
			want: true,
		},
		{
			name: "ASage service missing API key",
			service: ServiceConfig{
				ID:     "service-5",
				Type:   ServiceTypeASage,
				APIKey: "", // bad
			},
			want: false,
		},
		{
			name: "Valid Cohere service with API key",
			service: ServiceConfig{
				ID:     "service-6",
				Type:   ServiceTypeCohere,
				APIKey: "cohere-key",
			},
			want: true,
		},
		{
			name: "Cohere service missing API key",
			service: ServiceConfig{
				ID:     "service-6",
				Type:   ServiceTypeCohere,
				APIKey: "", // bad
			},
			want: false,
		},
		{
			name: "Service with empty ID",
			service: ServiceConfig{
				ID:     "", // bad
				Type:   ServiceTypeOpenAI,
				APIKey: "sk-xyz",
			},
			want: false,
		},
		{
			name: "Service with empty Type",
			service: ServiceConfig{
				ID:     "service-7",
				Type:   "", // bad
				APIKey: "sk-xyz",
			},
			want: false,
		},
		{
			name: "Service with unsupported Type",
			service: ServiceConfig{
				ID:     "service-8",
				Type:   "mattermostllm", // bad - unsupported
				APIKey: "sk-xyz",
			},
			want: false,
		},
		{
			name: "Service with invalid Type",
			service: ServiceConfig{
				ID:     "service-9",
				Type:   "unknown", // bad - unsupported
				APIKey: "sk-xyz",
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
