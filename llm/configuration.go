// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

// MaxCustomInstructionsRunes caps BotConfig.CustomInstructions at a length that keeps
// the system prompt bounded on every conversation turn.
const MaxCustomInstructionsRunes = 16384

type ServiceConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	APIKey       string `json:"apiKey"`
	OrgID        string `json:"orgId"`
	DefaultModel string `json:"defaultModel"`
	APIURL       string `json:"apiURL"`
	Region       string `json:"region"` // For AWS Bedrock region

	// AWS IAM credentials for Bedrock (optional, takes precedence over APIKey)
	AWSAccessKeyID     string `json:"awsAccessKeyID"`
	AWSSecretAccessKey string `json:"awsSecretAccessKey"`

	// Vertex AI (GCP) configuration. Region is reused from the shared Region field.
	// VertexAuthCredentials holds the service-account JSON; when empty, Bifrost
	// falls back to Application Default Credentials / attached IAM role.
	VertexProjectID       string `json:"vertexProjectID"`
	VertexProjectNumber   string `json:"vertexProjectNumber"`
	VertexAuthCredentials string `json:"vertexAuthCredentials"`

	// Renaming the JSON field to inputTokenLimit would require a migration, leaving as is for now.
	InputTokenLimit         int `json:"tokenLimit"`
	StreamingTimeoutSeconds int `json:"streamingTimeoutSeconds"`

	// Otherwise known as maxTokens
	OutputTokenLimit int `json:"outputTokenLimit"`

	// UseResponsesAPI determines whether to use the new OpenAI Responses API
	// Only applicable to OpenAI and OpenAI-compatible services
	UseResponsesAPI bool `json:"useResponsesAPI"`
}

// ServiceUsesResponsesAPI reports whether the Responses API path is used for this service.
// Direct OpenAI always uses the Responses API (PR #617); other types follow UseResponsesAPI.
func ServiceUsesResponsesAPI(cfg ServiceConfig) bool {
	if cfg.Type == ServiceTypeOpenAI {
		return true
	}
	return cfg.UseResponsesAPI
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

// EnabledMCPTool identifies a single MCP tool on a specific server (config bots and persisted agents).
type EnabledMCPTool struct {
	ServerOrigin string `json:"server_origin"`
	ToolName     string `json:"tool_name"`
}

type BotConfig struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	DisplayName        string `json:"displayName"`
	CustomInstructions string `json:"customInstructions"`
	ServiceID          string `json:"serviceID"`

	// Model is the optional model override for this bot.
	// If not specified, the service's DefaultModel will be used.
	Model string `json:"model"`

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

	// EnabledNativeTools contains the list of enabled native tools for this bot.
	// Supported values by provider:
	//   - OpenAI / Azure: ["web_search", "file_search", "code_interpreter"]
	//     (only works when UseResponsesAPI is true for OpenAI-compatible and Azure)
	//   - Anthropic: ["web_search"]
	//   - Gemini / Vertex AI: ["web_search"] (mapped to Google Search / grounding
	//     via Bifrost's Responses API)
	// For other providers these values are filtered out at request time.
	EnabledNativeTools []string `json:"enabledNativeTools"`

	// EnabledMCPTools is the per-agent allowlist of MCP tools:
	// only tools matching these (ServerOrigin, ToolName) pairs are kept.
	// Ignored when AutoEnableNewMCPTools is true.
	EnabledMCPTools []EnabledMCPTool `json:"enabledMCPTools"`

	// AutoEnableNewMCPTools, when true, gives this agent access to every currently
	// configured MCP tool and any MCP tool added later. EnabledMCPTools is ignored
	// in that mode. When false, only tools listed in EnabledMCPTools are available.
	AutoEnableNewMCPTools bool `json:"autoEnableNewMCPTools"`

	// ReasoningEnabled determines whether reasoning/thinking is enabled for this bot.
	// Applicable to OpenAI (with ResponsesAPI), Anthropic, and Gemini / Vertex AI.
	ReasoningEnabled bool `json:"reasoningEnabled"`

	// ReasoningEffort determines the reasoning effort level.
	// Valid values: "minimal", "low", "medium", "high".
	// Applicable to OpenAI (with ResponsesAPI) and Gemini / Vertex AI (maps to
	// Gemini's thinkingLevel on 3.0+, and to a thinkingBudget estimate on 2.5).
	// Default: "medium".
	ReasoningEffort string `json:"reasoningEffort"`

	// ThinkingBudget determines the token budget for reasoning/thinking.
	// - Anthropic: must be at least 1024 and cannot exceed the OutputTokenLimit.
	//   Default: 1/4 of OutputTokenLimit, capped at 8192.
	// - Gemini / Vertex AI: maps to thinkingConfig.thinkingBudget. When set, it
	//   takes priority over ReasoningEffort.
	ThinkingBudget int `json:"thinkingBudget"`

	// StructuredOutputEnabled enables structured JSON output for providers that support it.
	// When enabled, the provider will use the JSONOutputFormat schema from the request config
	// to constrain the model's output to valid JSON matching the schema.
	// Only applicable to Anthropic (Claude 4.5/4.6+ models)
	StructuredOutputEnabled bool `json:"structuredOutputEnabled"`

	// Admin / lifecycle metadata.
	BotUserID    string   `json:"botUserID,omitempty"`
	CreatorID    string   `json:"creatorID,omitempty"`
	AdminUserIDs []string `json:"adminUserIDs,omitempty"`
	CreateAt     int64    `json:"createAt,omitempty"`
	UpdateAt     int64    `json:"updateAt,omitempty"`
	DeleteAt     int64    `json:"deleteAt,omitempty"`
}

// Validate returns a descriptive error when the bot config is not valid. Service
// configuration is validated separately.
func (c *BotConfig) Validate() error {
	if c.Name == "" {
		return errors.New("name is required")
	}
	if c.DisplayName == "" {
		return errors.New("displayName is required")
	}
	if c.ServiceID == "" {
		return errors.New("serviceID is required")
	}
	if c.ChannelAccessLevel < ChannelAccessLevelAll || c.ChannelAccessLevel > ChannelAccessLevelNone {
		return errors.New("channelAccessLevel is out of range")
	}
	if c.UserAccessLevel < UserAccessLevelAll || c.UserAccessLevel > UserAccessLevelNone {
		return errors.New("userAccessLevel is out of range")
	}
	if utf8.RuneCountInString(c.CustomInstructions) > MaxCustomInstructionsRunes {
		return fmt.Errorf("customInstructions exceeds maximum length of %d characters", MaxCustomInstructionsRunes)
	}
	return nil
}

// IsValid reports whether the bot config is valid. Prefer Validate when a
// descriptive error is useful.
func (c *BotConfig) IsValid() bool {
	return c.Validate() == nil
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
	case ServiceTypeCohere:
		return service.APIKey != ""
	case ServiceTypeBedrock:
		// Bedrock requires AWS region
		// API key is optional as AWS credentials can come from environment/IAM role
		return service.Region != ""
	case ServiceTypeMistral:
		return service.APIKey != ""
	case ServiceTypeScale:
		return service.APIKey != "" && service.APIURL != ""
	case ServiceTypeGemini:
		return service.APIKey != ""
	case ServiceTypeVertex:
		// Auth credentials optional — empty means ADC / attached IAM role.
		if service.VertexProjectID == "" || service.Region == "" {
			return false
		}
		if service.VertexAuthCredentials == "" {
			return true
		}
		return json.Valid([]byte(service.VertexAuthCredentials))
	default:
		return false
	}
}

// IsCreator reports whether userID is the agent's creator.
// Returns false for migrated/config bots whose CreatorID is empty.
func (c *BotConfig) IsCreator(userID string) bool {
	if userID == "" || c.CreatorID == "" {
		return false
	}
	return c.CreatorID == userID
}

// IsAdmin reports whether userID is the agent's creator or in the admin list.
// Returns false for the empty userID to avoid matching legacy bots (CreatorID == "").
func (c *BotConfig) IsAdmin(userID string) bool {
	if userID == "" {
		return false
	}
	return c.IsCreator(userID) || slices.Contains(c.AdminUserIDs, userID)
}
