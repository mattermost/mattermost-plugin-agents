// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.enterprise for license information.

package enterprise

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var ErrNotLicensed = errors.New("license does not support this feature")

// Level is the plugin's view of the server license, ordered so that a higher
// level includes every capability of the levels below it.
type Level int

const (
	LevelUnlicensed Level = iota
	LevelProfessional
	LevelEnterprise
	LevelEnterpriseAdvanced
)

// String returns the admin-facing name of the level, used in error messages
// and the webapp mirror in webapp/src/license.tsx.
func (l Level) String() string {
	switch l {
	case LevelProfessional:
		return "Professional"
	case LevelEnterprise:
		return "Enterprise"
	case LevelEnterpriseAdvanced:
		return "Enterprise Advanced"
	default:
		return "Free"
	}
}

// Key returns the stable machine-readable identifier of the level carried in
// API error payloads.
func (l Level) Key() string {
	switch l {
	case LevelProfessional:
		return "professional"
	case LevelEnterprise:
		return "enterprise"
	case LevelEnterpriseAdvanced:
		return "enterprise_advanced"
	default:
		return "unlicensed"
	}
}

// Capability is a licensable plugin capability. Every gate in the plugin
// checks a Capability rather than a level so the tier chart lives in one place.
type Capability string

const (
	// Professional and above.
	CapMultiplayerChannels   Capability = "multiplayer_channels"
	CapThreadSummarization   Capability = "thread_summarization"
	CapChannelSummarization  Capability = "channel_summarization"
	CapProviderWebSearch     Capability = "provider_web_search"
	CapAgentAccessControls   Capability = "agent_access_controls"
	CapTokenAccounting       Capability = "token_accounting"
	CapSovereignWebSearch    Capability = "sovereign_web_search"
	CapToolApprovalPolicies  Capability = "tool_approval_policies"
	CapRemoteMCP             Capability = "remote_mcp"
	CapSemanticSearch        Capability = "semantic_search"
	CapMeetings              Capability = "meetings"
	CapMCPServiceAccount     Capability = "mcp_service_account"
	CapSharedPrompts         Capability = "shared_prompts"
	CapMultipleLLMServices   Capability = "multiple_llm_services"
	CapModelFallback         Capability = "model_fallback"
	CapStateChangingTools    Capability = "state_changing_tools"
	CapChannelAutoReply      Capability = "channel_auto_reply"
	CapAttributeBasedAccess  Capability = "attribute_based_access"
	capabilityUnknownDisplay            = "This feature"
)

type capabilitySpec struct {
	minLevel Level
	display  string
}

// capabilities is the tier chart: the minimum level at which each capability
// is available, plus the admin-facing name used in error messages.
var capabilities = map[Capability]capabilitySpec{
	CapMultiplayerChannels:  {LevelProfessional, "Multiplayer agents in channels"},
	CapThreadSummarization:  {LevelProfessional, "Thread summarization"},
	CapChannelSummarization: {LevelProfessional, "Channel and unread summarization"},
	CapProviderWebSearch:    {LevelProfessional, "Provider-native web search"},
	CapAgentAccessControls:  {LevelProfessional, "Agent access controls"},
	CapTokenAccounting:      {LevelProfessional, "Token accounting"},

	CapMultipleLLMServices:  {LevelEnterprise, "Multiple LLM services"},
	CapStateChangingTools:   {LevelEnterprise, "State-changing Mattermost tools"},
	CapSovereignWebSearch:   {LevelEnterprise, "Sovereign web search"},
	CapToolApprovalPolicies: {LevelEnterprise, "Tool approval policies"},
	CapRemoteMCP:            {LevelEnterprise, "Remote and external MCP servers"},
	CapSemanticSearch:       {LevelEnterprise, "Semantic AI search"},
	CapMeetings:             {LevelEnterprise, "Meeting transcription and summaries"},
	CapMCPServiceAccount:    {LevelEnterprise, "MCP service-account authentication"},
	CapSharedPrompts:        {LevelEnterprise, "Shared prompt libraries"},

	CapChannelAutoReply:     {LevelEnterpriseAdvanced, "Channel agent auto-reply"},
	CapAttributeBasedAccess: {LevelEnterpriseAdvanced, "Attribute-based access control"},
	CapModelFallback:        {LevelEnterpriseAdvanced, "LLM fallback chains"},
}

// RequiredLevel returns the minimum level at which capability is available. Unknown
// capabilities require Enterprise Advanced so a typo can never open a gate.
func RequiredLevel(capability Capability) Level {
	if spec, ok := capabilities[capability]; ok {
		return spec.minLevel
	}
	return LevelEnterpriseAdvanced
}

// DisplayName returns the admin-facing name of capability.
func DisplayName(capability Capability) string {
	if spec, ok := capabilities[capability]; ok {
		return spec.display
	}
	return capabilityUnknownDisplay
}

// Agent caps per level. Levels not listed here are uncapped.
const (
	FreeAgentLimit         = 1
	ProfessionalAgentLimit = 3
	// BaseServiceLimit is the number of LLM services available below Enterprise.
	BaseServiceLimit = 1
)

// LicenseError reports that a capability is unavailable at the current
// license level. It never carries license internals beyond the level names.
type LicenseError struct {
	Capability    Capability
	RequiredLevel Level
	CurrentLevel  Level
}

func (e *LicenseError) Error() string {
	return fmt.Sprintf("%s requires a Mattermost %s license or higher; the current license level is %s",
		DisplayName(e.Capability), e.RequiredLevel, e.CurrentLevel)
}

func (e *LicenseError) Unwrap() error { return ErrNotLicensed }

// NewLicenseError builds the error returned when capability is denied at current.
func NewLicenseError(capability Capability, current Level) *LicenseError {
	return &LicenseError{Capability: capability, RequiredLevel: RequiredLevel(capability), CurrentLevel: current}
}

// LicenseChecker resolves the server license into a Level and answers
// capability questions. A nil checker or nil plugin client always reports
// LevelUnlicensed so every gate fails closed.
type LicenseChecker struct {
	pluginAPIClient *pluginapi.Client
}

func NewLicenseChecker(pluginAPIClient *pluginapi.Client) *LicenseChecker {
	return &LicenseChecker{
		pluginAPIClient,
	}
}

// Level returns the current license level. A development server (EnableTesting
// and EnableDeveloper both on) reports LevelEnterpriseAdvanced so every
// capability can be exercised locally.
func (e *LicenseChecker) Level() Level {
	if e == nil || e.pluginAPIClient == nil {
		return LevelUnlicensed
	}
	config := e.pluginAPIClient.Configuration.GetConfig()
	license := e.pluginAPIClient.System.GetLicense()
	return LevelFor(config, license)
}

// LevelFor maps a server config and license to a Level. The Entry SKU maps to
// Enterprise. Licenses with an unknown SKU fall back to feature flags, matching
// the heuristics in pluginapi.
func LevelFor(config *model.Config, license *model.License) Level {
	if pluginapi.IsConfiguredForDevelopment(config) {
		return LevelEnterpriseAdvanced
	}
	if license == nil {
		return LevelUnlicensed
	}
	switch license.SkuShortName {
	case model.LicenseShortSkuEnterpriseAdvanced:
		return LevelEnterpriseAdvanced
	case model.LicenseShortSkuEnterprise, model.LicenseShortSkuMattermostEntry, model.LicenseShortSkuE20:
		return LevelEnterprise
	case model.LicenseShortSkuProfessional, model.LicenseShortSkuE10:
		return LevelProfessional
	}
	if license.Features != nil {
		if license.Features.FutureFeatures != nil && *license.Features.FutureFeatures {
			return LevelEnterprise
		}
		if license.Features.LDAP != nil && *license.Features.LDAP {
			return LevelProfessional
		}
	}
	return LevelUnlicensed
}

// HasLevel reports whether the current level is at least minimum.
func (e *LicenseChecker) HasLevel(minimum Level) bool {
	return e.Level() >= minimum
}

// Allows reports whether capability is available at the current level.
func (e *LicenseChecker) Allows(capability Capability) bool {
	return e.Level() >= RequiredLevel(capability)
}

// Check returns nil when capability is available, or a *LicenseError
// describing the required level otherwise.
func (e *LicenseChecker) Check(capability Capability) error {
	current := e.Level()
	if current >= RequiredLevel(capability) {
		return nil
	}
	return NewLicenseError(capability, current)
}

// AgentLimit returns the maximum number of active agents (configuration-file
// bots and user-created agents together) at the current level. ok is false
// when agents are uncapped.
func (e *LicenseChecker) AgentLimit() (limit int, ok bool) {
	return AgentLimitFor(e.Level())
}

// AgentLimitFor returns the agent cap for level; ok is false when uncapped.
func AgentLimitFor(level Level) (limit int, ok bool) {
	switch {
	case level >= LevelEnterprise:
		return 0, false
	case level >= LevelProfessional:
		return ProfessionalAgentLimit, true
	default:
		return FreeAgentLimit, true
	}
}

// ServiceLimit returns the maximum number of LLM services at the current
// level. ok is false when services are uncapped.
func (e *LicenseChecker) ServiceLimit() (limit int, ok bool) {
	return ServiceLimitFor(e.Level())
}

// ServiceLimitFor returns the LLM service cap for level; ok is false when uncapped.
func ServiceLimitFor(level Level) (limit int, ok bool) {
	if level >= LevelEnterprise {
		return 0, false
	}
	return BaseServiceLimit, true
}

// IsMultiLLMLicensed reports whether multiple LLM services and per-agent
// service routing are available.
func (e *LicenseChecker) IsMultiLLMLicensed() bool {
	return e.Allows(CapMultipleLLMServices)
}

// IsBasicsLicensed reports whether the Enterprise capability set is available.
func (e *LicenseChecker) IsBasicsLicensed() bool {
	return e.HasLevel(LevelEnterprise)
}

// AgentLimitError reports that another AI agent is not available at current.
// Uncapped levels return nil. abortNotLicensed unwraps the nested *LicenseError
// for license_required while Error names the numeric cap and the level that
// raises it (Professional raises the cap to 3; Enterprise removes it).
func AgentLimitError(current Level) error {
	limit, ok := AgentLimitFor(current)
	if !ok {
		return nil
	}
	required := LevelProfessional
	if current >= LevelProfessional {
		required = LevelEnterprise
	}
	return &agentLimitError{
		lic:   LicenseError{RequiredLevel: required, CurrentLevel: current},
		Limit: limit,
	}
}

type agentLimitError struct {
	lic   LicenseError
	Limit int
}

func (e *agentLimitError) Error() string {
	return fmt.Sprintf("the current license level allows %d AI agents; more agents require a Mattermost %s license", e.Limit, e.lic.RequiredLevel)
}

func (e *agentLimitError) Unwrap() error {
	return &e.lic
}
