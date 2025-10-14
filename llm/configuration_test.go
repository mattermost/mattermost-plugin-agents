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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: false,
		},
		{
			name: "Service type must be one of the supported providers",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "mattermostllm", // bad
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openai",
					APIKey:                  "sk-xyz",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelNone + 1, // bad
			},
			want: false,
		},
		{
			name: "OpenAI compatible service requires API URL to be set",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openaicompatible",
					APIKey:                  "sk-xyz",
					APIURL:                  "", // bad
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
				ChannelAccessLevel: ChannelAccessLevelAll,
				UserAccessLevel:    UserAccessLevelAll,
			},
			want: false,
		},
		{
			name: "OpenAI compatible service do not requires API Key to be set",
			fields: fields{
				ID:                 "xxx",
				Name:               "xxx",
				DisplayName:        "xxx",
				CustomInstructions: "",
				Service: &ServiceConfig{
					Name:                    "Agents",
					Type:                    "openaicompatible",
					APIKey:                  "", // not bad
					APIURL:                  "http://localhost",
					OrgID:                   "org-xyz",
					DefaultModel:            "gpt-40",
					InputTokenLimit:         100,
					StreamingTimeoutSeconds: 60,
				},
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
