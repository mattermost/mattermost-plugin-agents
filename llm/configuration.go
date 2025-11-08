// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

type ServiceConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	APIKey       string `json:"apiKey"`
	OrgID        string `json:"orgId"`
	DefaultModel string `json:"defaultModel"`
	APIURL       string `json:"apiURL"`

	// Renaming the JSON field to inputTokenLimit would require a migration, leaving as is for now.
	InputTokenLimit         int  `json:"tokenLimit"`
	StreamingTimeoutSeconds int  `json:"streamingTimeoutSeconds"`
	SendUserID              bool `json:"sendUserID"`

	// Otherwise known as maxTokens
	OutputTokenLimit int `json:"outputTokenLimit"`

	// UseResponsesAPI determines whether to use the new OpenAI Responses API
	// Only applicable to OpenAI and OpenAI-compatible services
	UseResponsesAPI bool `json:"useResponsesAPI"`
}

type ChannelAccessLevel int

const (
	ChannelAccessLevelAll ChannelAccessLevel = iota
	ChannelAccessLevelAllow
	ChannelAccessLevelBlock
	ChannelAccessLevelNone
)

type UserAccessLevel int

const (
	UserAccessLevelAll UserAccessLevel = iota
	UserAccessLevelAllow
	UserAccessLevelBlock
	UserAccessLevelNone
)

type BotConfig struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	DisplayName        string `json:"displayName"`
	CustomInstructions string `json:"customInstructions"`
	ServiceID          string `json:"serviceID"`

	// Service is deprecated and kept only for backwards compatibility during migration.
	Service *ServiceConfig `json:"service,omitempty"`

	EnableVision       bool               `json:"enableVision"`
	DisableTools       bool               `json:"disableTools"`
	ChannelAccessLevel ChannelAccessLevel `json:"channelAccessLevel"`
	ChannelIDs         []string           `json:"channelIDs"`
	UserAccessLevel    UserAccessLevel    `json:"userAccessLevel"`
	UserIDs            []string           `json:"userIDs"`
	TeamIDs            []string           `json:"teamIDs"`
	MaxFileSize        int64              `json:"maxFileSize"`

	// EnabledNativeTools contains the list of enabled native tools for this bot
	// For OpenAI: ["web_search", "file_search", "code_interpreter"] (only works when UseResponsesAPI is true)
	// For Anthropic: ["web_search"]
	EnabledNativeTools []string `json:"enabledNativeTools"`

	// ReasoningEnabled determines whether reasoning/thinking is enabled for this bot
	// Applicable to OpenAI (with ResponsesAPI) and Anthropic
	ReasoningEnabled bool `json:"reasoningEnabled"`

	// ReasoningEffort determines the reasoning effort level for OpenAI models
	// Valid values: "minimal", "low", "medium", "high"
	// Only applicable to OpenAI with ResponsesAPI enabled
	// Default: "medium"
	ReasoningEffort string `json:"reasoningEffort"`

	// ThinkingBudget determines the token budget for Anthropic thinking
	// Must be at least 1024 and cannot exceed the OutputTokenLimit
	// Only applicable to Anthropic
	// Default: 1/4 of OutputTokenLimit, capped at 8192
	ThinkingBudget int `json:"thinkingBudget"`
}

func (c *BotConfig) IsValid() bool {
	// Basic validation - service validation happens separately
	if c.Name == "" || c.DisplayName == "" || c.ServiceID == "" {
		return false
	}

	// Validate access levels are within bounds
	if c.ChannelAccessLevel < ChannelAccessLevelAll || c.ChannelAccessLevel > ChannelAccessLevelNone {
		return false
	}
	if c.UserAccessLevel < UserAccessLevelAll || c.UserAccessLevel > UserAccessLevelNone {
		return false
	}

	return true
}

// IsValidService validates a service configuration
func IsValidService(service ServiceConfig) bool {
	// Basic validation
	if service.ID == "" || service.Type == "" {
		return false
	}

	// Service-specific validation
	switch service.Type {
	case ServiceTypeOpenAI:
		return service.APIKey != ""
	case ServiceTypeOpenAICompatible:
		return service.APIURL != ""
	case ServiceTypeAzure:
		return service.APIKey != "" && service.APIURL != ""
	case ServiceTypeAnthropic:
		return service.APIKey != ""
	case ServiceTypeASage:
		return service.APIKey != ""
	case ServiceTypeCohere:
		return service.APIKey != ""
	default:
		return false
	}
}
