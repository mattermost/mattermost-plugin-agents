// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/loadtest/profile"
)

// MaxCustomInstructionsRunes bounds the per-turn LLM system prompt and agent-save
// request. Custom instructions are sent only to the LLM, not as Mattermost posts.
const MaxCustomInstructionsRunes = 100000

// DefaultMaxToolTurns is the default tool-call-execute-recall ceiling per LLM turn.
// Agents that store 0 (legacy config bots, freshly-migrated rows before the column
// was added) fall back to this value via BotConfig.EffectiveMaxToolTurns.
const DefaultMaxToolTurns = 30

// MaxAllowedMaxToolTurns caps user-provided MaxToolTurns to keep runaway loops bounded
// even if a misconfigured agent requests an unreasonably high value.
const MaxAllowedMaxToolTurns = 250

// StructuredOutputPolicy declares how a service handles a requested
// JSONOutputFormat schema. It is stored per service because the capability
// belongs to the provider/model, not to the agent asking for JSON.
type StructuredOutputPolicy string

const (
	// StructuredOutputPolicyAuto lets the plugin decide from the service type,
	// API path, and model (see bifrost.ResolveStructuredOutputCapability).
	// Anything not positively known to support native schemas uses the prompt
	// fallback. This is the value an empty/unset policy maps to.
	StructuredOutputPolicyAuto StructuredOutputPolicy = "auto"
	// StructuredOutputPolicyNative asserts that the provider/model accepts a
	// native JSON schema. The admin takes responsibility for the assertion.
	StructuredOutputPolicyNative StructuredOutputPolicy = "native"
	// StructuredOutputPolicyPromptFallback always converts the schema into a
	// prompt instruction and never sends it to the provider.
	StructuredOutputPolicyPromptFallback StructuredOutputPolicy = "prompt_fallback"
)

// IsValidStructuredOutputPolicy reports whether the stored value is one the
// runtime understands. The empty value is valid and means "auto".
func IsValidStructuredOutputPolicy(policy StructuredOutputPolicy) bool {
	switch policy {
	case "", StructuredOutputPolicyAuto, StructuredOutputPolicyNative, StructuredOutputPolicyPromptFallback:
		return true
	default:
		return false
	}
}

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

	// FallbackServiceID is the ID of another service to fall back to when this
	// service's provider/model fails (e.g. network error, rate limit, model
	// unavailable). The fallback service's DefaultModel is used. Chains are
	// followed (A→B→C) with cycle detection.
	FallbackServiceID string `json:"fallbackServiceID,omitempty"`

	// LoadTestMockConfig is raw JSON merged by loadtest.ParseProfile for ServiceTypeLoadTestMock.
	// Nil, empty, or whitespace-only means the default read/search-heavy profile.
	LoadTestMockConfig json.RawMessage `json:"loadTestMockConfig,omitempty"`

	// StructuredOutputPolicy controls how a requested JSON schema reaches this
	// service. An empty value means StructuredOutputPolicyAuto, so services
	// stored before this field existed need no migration.
	StructuredOutputPolicy StructuredOutputPolicy `json:"structuredOutputPolicy,omitempty"`
}

// EffectiveStructuredOutputPolicy returns the configured policy, mapping the
// empty value to auto.
func (c ServiceConfig) EffectiveStructuredOutputPolicy() StructuredOutputPolicy {
	if c.StructuredOutputPolicy == "" {
		return StructuredOutputPolicyAuto
	}
	return c.StructuredOutputPolicy
}

// ServiceUsesResponsesAPI reports whether the Responses API path is used for this service.
// Direct OpenAI always uses it; OpenAI-compatible and Azure honor the operator toggle.
// All other service types ignore UseResponsesAPI — a stale flag carried over from a
// previous service type must not be allowed to route the request through Responses.
func ServiceUsesResponsesAPI(cfg ServiceConfig) bool {
	switch cfg.Type {
	case ServiceTypeOpenAI:
		return true
	case ServiceTypeOpenAICompatible, ServiceTypeAzure:
		return cfg.UseResponsesAPI
	default:
		return false
	}
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

	// EnabledNativeTools contains the list of enabled native tools for this bot
	// (see the NativeTool* constants). Which ids a provider actually supports is
	// defined by bifrost.SupportedNativeToolsForServiceType; unsupported values
	// are filtered out at request time. For OpenAI-compatible and Azure services,
	// native tools additionally require UseResponsesAPI.
	EnabledNativeTools []string `json:"enabledNativeTools"`

	// EnabledMCPTools is the per-agent allowlist of MCP tools:
	// only tools matching these (ServerOrigin, ToolName) pairs are kept.
	// Ignored when AutoEnableNewMCPTools is true.
	EnabledMCPTools []EnabledMCPTool `json:"enabledMCPTools"`

	// AutoEnableNewMCPTools, when true, gives this agent access to every currently
	// configured MCP tool and any MCP tool added later. EnabledMCPTools is ignored
	// in that mode. When false, only tools listed in EnabledMCPTools are available.
	AutoEnableNewMCPTools bool `json:"autoEnableNewMCPTools"`

	// MCPDynamicToolLoading controls whether this bot uses the JIT MCP tool loading flow.
	// It defaults to true for omitted legacy config.
	MCPDynamicToolLoading bool `json:"mcpDynamicToolLoading"`

	// UseServiceAccountAuth switches external MCP access for this agent to
	// admin-configured ServiceAccountHeaders instead of per-user OAuth.
	// Embedded Mattermost and plugin MCP servers still run as the requesting user.
	UseServiceAccountAuth bool `json:"useServiceAccountAuth"`

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

	// StructuredOutputEnabled is deprecated and ignored at runtime. Structured
	// output is decided per service by ServiceConfig.StructuredOutputPolicy
	// (see EffectiveStructuredOutputPolicy), because the capability belongs to
	// the provider/model rather than the agent.
	//
	// The field is retained so an API payload that still carries it is accepted,
	// but the current webapp omits it: since this is a plain bool, saving an
	// agent from the UI clears whatever was stored. Nothing reads it at runtime,
	// so the only consumer is the activation migration that carries the old
	// intent over to the service policy
	// (config.MigrateServiceStructuredOutputPolicies).
	//
	// Deprecated: use ServiceConfig.StructuredOutputPolicy.
	StructuredOutputEnabled bool `json:"structuredOutputEnabled"`

	// MaxToolTurns is the maximum number of LLM-call → tool-execute iterations
	// the tool runner will perform for this agent before stopping. A non-positive
	// value falls back to DefaultMaxToolTurns. Lower this when using a smaller
	// model that tends to call tools in loops; raise it for agents that rely on
	// long dynamic-tool-discovery chains (e.g. search → load → execute).
	MaxToolTurns int `json:"maxToolTurns"`

	// Admin / lifecycle metadata.
	BotUserID    string   `json:"botUserID,omitempty"`
	CreatorID    string   `json:"creatorID,omitempty"`
	AdminUserIDs []string `json:"adminUserIDs,omitempty"`
	CreateAt     int64    `json:"createAt,omitempty"`
	UpdateAt     int64    `json:"updateAt,omitempty"`
	DeleteAt     int64    `json:"deleteAt,omitempty"`
}

func (c *BotConfig) UnmarshalJSON(data []byte) error {
	type botConfigAlias BotConfig
	defaults := botConfigAlias{
		MCPDynamicToolLoading: true,
	}
	if err := json.Unmarshal(data, &defaults); err != nil {
		return err
	}
	*c = BotConfig(defaults)
	return nil
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
	if c.MaxToolTurns < 0 {
		return errors.New("maxToolTurns must be greater than or equal to 0")
	}
	if c.MaxToolTurns > MaxAllowedMaxToolTurns {
		return fmt.Errorf("maxToolTurns must be less than or equal to %d", MaxAllowedMaxToolTurns)
	}
	return nil
}

// EffectiveMaxToolTurns returns the configured MaxToolTurns or DefaultMaxToolTurns
// when the value is non-positive (e.g. legacy config bots that never set the field).
func (c BotConfig) EffectiveMaxToolTurns() int {
	if c.MaxToolTurns <= 0 {
		return DefaultMaxToolTurns
	}
	return c.MaxToolTurns
}

// IsValid reports whether the bot config is valid. Prefer Validate when a
// descriptive error is useful.
func (c *BotConfig) IsValid() bool {
	return c.Validate() == nil
}

// ServiceLookup returns a by-ID lookup over services, suitable for
// ResolveFallbackChain. Duplicate IDs resolve to the first entry, matching how
// the configuration itself is read.
func ServiceLookup(services []ServiceConfig) func(id string) (ServiceConfig, bool) {
	byID := make(map[string]ServiceConfig, len(services))
	for _, svc := range services {
		if _, exists := byID[svc.ID]; !exists {
			byID[svc.ID] = svc
		}
	}
	return func(id string) (ServiceConfig, bool) {
		svc, ok := byID[id]
		return svc, ok
	}
}

// ResolveFallbackChain walks the fallback chain starting from the service
// identified by primaryServiceID, returning an ordered slice of fallback
// ServiceConfigs. A misconfigured chain — a cycle, or a fallback ID that is
// missing or invalid — returns an error so the problem surfaces at setup
// instead of silently leaving the bot without the configured fallback. A
// missing primary returns an empty chain; the caller surfaces that error when
// it resolves the primary itself.
func ResolveFallbackChain(primaryServiceID string, getServiceByID func(id string) (ServiceConfig, bool)) ([]ServiceConfig, error) {
	primarySvc, ok := getServiceByID(primaryServiceID)
	if !ok {
		return nil, nil
	}

	var chain []ServiceConfig
	visited := map[string]bool{primaryServiceID: true}
	currentID := primarySvc.FallbackServiceID

	for currentID != "" {
		if visited[currentID] {
			return nil, fmt.Errorf("fallback chain of service %q contains a cycle at service %q", primaryServiceID, currentID)
		}
		visited[currentID] = true

		svc, ok := getServiceByID(currentID)
		if !ok {
			return nil, fmt.Errorf("fallback service %q in the chain of service %q does not exist", currentID, primaryServiceID)
		}
		if !IsValidService(svc) {
			return nil, fmt.Errorf("fallback service %q in the chain of service %q has an invalid configuration", currentID, primaryServiceID)
		}

		chain = append(chain, svc)
		currentID = svc.FallbackServiceID
	}

	return chain, nil
}

// IsValidService validates a service configuration
func IsValidService(service ServiceConfig) bool {
	// Basic validation
	if service.ID == "" || service.Type == "" {
		return false
	}

	// An unrecognized structured-output policy is a configuration error: the
	// runtime would have to guess whether a schema may be sent natively.
	if !IsValidStructuredOutputPolicy(service.StructuredOutputPolicy) {
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
	case ServiceTypeLoadTestMock:
		_, err := profile.Parse(service.LoadTestMockConfig)
		return err == nil
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
