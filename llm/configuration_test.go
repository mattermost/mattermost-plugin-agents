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
			name: "Valid Bedrock service with region",
			service: ServiceConfig{
				ID:     "service-7",
				Type:   ServiceTypeBedrock,
				Region: "us-east-1", // AWS region
			},
			want: true,
		},
		{
			name: "Bedrock service missing region",
			service: ServiceConfig{
				ID:     "service-7",
				Type:   ServiceTypeBedrock,
				Region: "", // bad - region required
			},
			want: false,
		},
		{
			name: "Bedrock service does not require API key",
			service: ServiceConfig{
				ID:     "service-7",
				Type:   ServiceTypeBedrock,
				APIKey: "", // not required - can use IAM role
				Region: "us-west-2",
			},
			want: true,
		},
		{
			name: "Valid Mistral service with API key",
			service: ServiceConfig{
				ID:     "service-8",
				Type:   ServiceTypeMistral,
				APIKey: "mistral-key",
			},
			want: true,
		},
		{
			name: "Mistral service missing API key",
			service: ServiceConfig{
				ID:     "service-8",
				Type:   ServiceTypeMistral,
				APIKey: "", // bad
			},
			want: false,
		},
		{
			name: "Valid Scale service with API key and API URL",
			service: ServiceConfig{
				ID:     "service-9",
				Type:   ServiceTypeScale,
				APIKey: "scale-key",
				APIURL: "https://sgp-api.scalegov.com/v5",
			},
			want: true,
		},
		{
			name: "Valid Scale service with API key, API URL, and OrgID",
			service: ServiceConfig{
				ID:     "service-9",
				Type:   ServiceTypeScale,
				APIKey: "scale-key",
				APIURL: "https://sgp-api.scalegov.com/v5",
				OrgID:  "account-123",
			},
			want: true,
		},
		{
			name: "Scale service missing API key",
			service: ServiceConfig{
				ID:     "service-9",
				Type:   ServiceTypeScale,
				APIKey: "", // bad
				APIURL: "https://sgp-api.scalegov.com/v5",
			},
			want: false,
		},
		{
			name: "Scale service missing API URL",
			service: ServiceConfig{
				ID:     "service-9",
				Type:   ServiceTypeScale,
				APIKey: "scale-key",
				APIURL: "", // bad
			},
			want: false,
		},
		{
			name: "Valid Gemini service with API key",
			service: ServiceConfig{
				ID:     "service-10",
				Type:   ServiceTypeGemini,
				APIKey: "gemini-key",
			},
			want: true,
		},
		{
			name: "Gemini service missing API key",
			service: ServiceConfig{
				ID:     "service-10",
				Type:   ServiceTypeGemini,
				APIKey: "", // bad
			},
			want: false,
		},
		{
			name: "Valid Vertex service with ADC (no credentials)",
			service: ServiceConfig{
				ID:              "service-11",
				Type:            ServiceTypeVertex,
				VertexProjectID: "my-project",
				Region:          "us-central1",
				// VertexAuthCredentials empty — ADC / IAM role path
			},
			want: true,
		},
		{
			name: "Valid Vertex service with service account JSON",
			service: ServiceConfig{
				ID:                    "service-11",
				Type:                  ServiceTypeVertex,
				VertexProjectID:       "my-project",
				Region:                "europe-west4",
				VertexAuthCredentials: `{"type":"service_account"}`,
			},
			want: true,
		},
		{
			name: "Vertex service missing project ID",
			service: ServiceConfig{
				ID:              "service-11",
				Type:            ServiceTypeVertex,
				VertexProjectID: "", // bad
				Region:          "us-central1",
			},
			want: false,
		},
		{
			name: "Vertex service missing region",
			service: ServiceConfig{
				ID:              "service-11",
				Type:            ServiceTypeVertex,
				VertexProjectID: "my-project",
				Region:          "", // bad
			},
			want: false,
		},
		{
			name: "Vertex service with invalid service account JSON",
			service: ServiceConfig{
				ID:                    "service-11",
				Type:                  ServiceTypeVertex,
				VertexProjectID:       "my-project",
				Region:                "us-central1",
				VertexAuthCredentials: `{not-json`, // bad
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
		{
			name: "Valid loadtest mock service minimal",
			service: ServiceConfig{
				ID:   "loadtest",
				Type: ServiceTypeLoadTestMock,
			},
			want: true,
		},
		{
			name: "Valid loadtest mock service with profile JSON",
			service: ServiceConfig{
				ID:                 "loadtest",
				Type:               ServiceTypeLoadTestMock,
				LoadTestMockConfig: json.RawMessage(`{"profile_weights":{"realistic_default":1,"realistic_fast":0,"realistic_slow":0}}`),
			},
			want: true,
		},
		{
			name: "Invalid loadtest mock service missing ID",
			service: ServiceConfig{
				Type: ServiceTypeLoadTestMock,
			},
			want: false,
		},
		{
			name: "Invalid loadtest mock service malformed JSON config",
			service: ServiceConfig{
				ID:                 "loadtest",
				Type:               ServiceTypeLoadTestMock,
				LoadTestMockConfig: json.RawMessage(`{`),
			},
			want: false,
		},
		{
			name: "Invalid loadtest mock service unknown profile field",
			service: ServiceConfig{
				ID:                 "loadtest",
				Type:               ServiceTypeLoadTestMock,
				LoadTestMockConfig: json.RawMessage(`{"unknown_top_level":true}`),
			},
			want: false,
		},
		{
			name: "Invalid loadtest mock service unknown latency profile weight",
			service: ServiceConfig{
				ID:                 "loadtest",
				Type:               ServiceTypeLoadTestMock,
				LoadTestMockConfig: json.RawMessage(`{"profile_weights":{"does_not_exist":1}}`),
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

func TestBotConfigMCPDynamicToolLoadingDefaulting(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{
			name:    "omitted defaults true",
			payload: `{"id":"bot1","name":"bot1","displayName":"Bot One","serviceID":"svc-1"}`,
			want:    true,
		},
		{
			name:    "explicit false survives",
			payload: `{"id":"bot1","name":"bot1","displayName":"Bot One","serviceID":"svc-1","mcpDynamicToolLoading":false}`,
			want:    false,
		},
		{
			name:    "explicit true survives",
			payload: `{"id":"bot1","name":"bot1","displayName":"Bot One","serviceID":"svc-1","mcpDynamicToolLoading":true}`,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg BotConfig
			require.NoError(t, json.Unmarshal([]byte(tt.payload), &cfg))
			assert.Equal(t, tt.want, cfg.MCPDynamicToolLoading)
		})
	}

	raw, err := json.Marshal(BotConfig{MCPDynamicToolLoading: false})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"mcpDynamicToolLoading":false`)
}

func TestServiceConfig_JSONRoundTrip_FallbackServiceID(t *testing.T) {
	cfg := ServiceConfig{
		ID:                "s1",
		Type:              ServiceTypeOpenAI,
		APIKey:            "key",
		FallbackServiceID: "s2",
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"fallbackServiceID":"s2"`)

	var decoded ServiceConfig
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "s2", decoded.FallbackServiceID)
}

func TestServiceConfig_JSONRoundTrip_FallbackServiceID_Omitted(t *testing.T) {
	cfg := ServiceConfig{
		ID:     "s1",
		Type:   ServiceTypeOpenAI,
		APIKey: "key",
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "fallbackServiceID")
}

func TestResolveFallbackChain(t *testing.T) {
	openAISvc := ServiceConfig{
		ID:                "openai-1",
		Type:              ServiceTypeOpenAI,
		APIKey:            "key-openai",
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "anthropic-1",
	}
	anthropicSvc := ServiceConfig{
		ID:                "anthropic-1",
		Type:              ServiceTypeAnthropic,
		APIKey:            "key-anthropic",
		DefaultModel:      "claude-sonnet-4-20250514",
		FallbackServiceID: "local-1",
	}
	localSvc := ServiceConfig{
		ID:           "local-1",
		Type:         ServiceTypeOpenAICompatible,
		APIURL:       "http://localhost:11434/v1",
		DefaultModel: "llama3",
	}
	cycleSvcA := ServiceConfig{
		ID:                "cycle-a",
		Type:              ServiceTypeOpenAI,
		APIKey:            "key-a",
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "cycle-b",
	}
	cycleSvcB := ServiceConfig{
		ID:                "cycle-b",
		Type:              ServiceTypeAnthropic,
		APIKey:            "key-b",
		DefaultModel:      "claude-sonnet-4-20250514",
		FallbackServiceID: "cycle-a",
	}
	selfCycleSvc := ServiceConfig{
		ID:                "self-cycle",
		Type:              ServiceTypeOpenAI,
		APIKey:            "key",
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "self-cycle",
	}
	invalidFallbackSvc := ServiceConfig{
		ID:                "valid-primary",
		Type:              ServiceTypeOpenAI,
		APIKey:            "key",
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "invalid-svc",
	}
	// Invalid service (missing required API key)
	invalidSvc := ServiceConfig{
		ID:   "invalid-svc",
		Type: ServiceTypeOpenAI,
		// APIKey intentionally missing
	}
	noFallbackSvc := ServiceConfig{
		ID:           "no-fallback",
		Type:         ServiceTypeOpenAI,
		APIKey:       "key",
		DefaultModel: "gpt-4o",
	}
	// A valid primary whose fallback ID does not resolve to any service, so the
	// chain stops at the primary instead of exercising the "primary not found" path.
	missingFallbackSvc := ServiceConfig{
		ID:                "missing-fallback-primary",
		Type:              ServiceTypeOpenAI,
		APIKey:            "key",
		DefaultModel:      "gpt-4o",
		FallbackServiceID: "does-not-exist",
	}

	allServices := map[string]ServiceConfig{
		openAISvc.ID:          openAISvc,
		anthropicSvc.ID:       anthropicSvc,
		localSvc.ID:           localSvc,
		cycleSvcA.ID:          cycleSvcA,
		cycleSvcB.ID:          cycleSvcB,
		selfCycleSvc.ID:       selfCycleSvc,
		invalidFallbackSvc.ID: invalidFallbackSvc,
		invalidSvc.ID:         invalidSvc,
		noFallbackSvc.ID:      noFallbackSvc,
		missingFallbackSvc.ID: missingFallbackSvc,
	}

	lookup := func(id string) (ServiceConfig, bool) {
		svc, ok := allServices[id]
		return svc, ok
	}

	tests := []struct {
		name           string
		primaryID      string
		expectedIDs    []string
		expectedModels []string
		expectErr      string
	}{
		{
			name:        "no fallback configured",
			primaryID:   noFallbackSvc.ID,
			expectedIDs: nil,
		},
		{
			name:           "simple chain A→B",
			primaryID:      anthropicSvc.ID,
			expectedIDs:    []string{"local-1"},
			expectedModels: []string{"llama3"},
		},
		{
			name:           "multi-hop chain A→B→C",
			primaryID:      openAISvc.ID,
			expectedIDs:    []string{"anthropic-1", "local-1"},
			expectedModels: []string{"claude-sonnet-4-20250514", "llama3"},
		},
		{
			name:      "cycle A→B→A errors",
			primaryID: cycleSvcA.ID,
			expectErr: "cycle",
		},
		{
			name:      "self-cycle A→A errors",
			primaryID: selfCycleSvc.ID,
			expectErr: "cycle",
		},
		{
			name:      "fallback to invalid service errors",
			primaryID: invalidFallbackSvc.ID,
			expectErr: "invalid configuration",
		},
		{
			name:        "primary not found returns nil without error",
			primaryID:   "nonexistent",
			expectedIDs: nil,
		},
		{
			name:      "fallback points to missing service errors",
			primaryID: "missing-fallback-primary",
			expectErr: "does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain, err := ResolveFallbackChain(tt.primaryID, lookup)

			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)

			assert.Len(t, chain, len(tt.expectedIDs))

			if tt.expectedIDs != nil {
				gotIDs := make([]string, len(chain))
				for i, svc := range chain {
					gotIDs[i] = svc.ID
				}
				assert.Equal(t, tt.expectedIDs, gotIDs)
			}

			if tt.expectedModels != nil {
				gotModels := make([]string, len(chain))
				for i, svc := range chain {
					gotModels[i] = svc.DefaultModel
				}
				assert.Equal(t, tt.expectedModels, gotModels)
			}
		})
	}
}
