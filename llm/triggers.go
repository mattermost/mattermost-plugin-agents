// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

// SubscriptionEventMessagePosted is the only event supported in V1.
// It fires whenever a non-automated post appears in ScopeChannelID.
const SubscriptionEventMessagePosted = "message_posted"

// MinScheduleIntervalSeconds caps schedule frequency so an agent cannot hammer
// an LLM service. 1 hour is the lowest we support in V1; admins who want
// shorter intervals should make the case before the limit is raised.
const MinScheduleIntervalSeconds = 3600

// MaxTriggerPromptRunes is a per-trigger prompt cap independent of the bot's
// CustomInstructions cap. Triggers receive their own budget because they run
// without a human in the loop and a verbose prompt becomes an operational risk.
const MaxTriggerPromptRunes = 8192

// ReservedBoundToolNames lists tools that must never appear in a trigger's
// AllowedTools. `create_post_as_user` posts as a real human user, which
// breaks the bot-identity invariant every scoped run relies on.
var ReservedBoundToolNames = []string{"create_post_as_user"}

// BoundParamTargetChannelSentinel is the literal string the webapp writes into
// BoundParams for tools that should post to TargetChannelID. The dispatcher
// swaps it for the trigger's resolved TargetChannelID at run time.
const BoundParamTargetChannelSentinel = "{{TargetChannelID}}"

// AgentSubscription binds an agent to an event (V1: message_posted on a
// specific channel) with a scoped run configuration. It lives inline on the
// agent row as JSON; see store/agents.go for persistence.
type AgentSubscription struct {
	ID              string                            `json:"id"`
	Event           string                            `json:"event"`
	ScopeChannelID  string                            `json:"scopeChannelID"`
	Prompt          string                            `json:"prompt"`
	TargetChannelID string                            `json:"targetChannelID"`
	AllowedTools    []string                          `json:"allowedTools"`
	BoundParams     map[string]map[string]interface{} `json:"boundParams,omitempty"`
	Enabled         bool                              `json:"enabled"`
	LastFireAt      int64                             `json:"lastFireAt,omitempty"`
	LastError       string                            `json:"lastError,omitempty"`
	LastErrorAt     int64                             `json:"lastErrorAt,omitempty"`
}

// AgentSchedule runs an agent on a fixed interval.
type AgentSchedule struct {
	ID              string                            `json:"id"`
	IntervalSeconds int64                             `json:"intervalSeconds"`
	Prompt          string                            `json:"prompt"`
	TargetChannelID string                            `json:"targetChannelID"`
	AllowedTools    []string                          `json:"allowedTools"`
	BoundParams     map[string]map[string]interface{} `json:"boundParams,omitempty"`
	Enabled         bool                              `json:"enabled"`
	NextFireAt      int64                             `json:"nextFireAt,omitempty"`
	LastFireAt      int64                             `json:"lastFireAt,omitempty"`
	LastError       string                            `json:"lastError,omitempty"`
	LastErrorAt     int64                             `json:"lastErrorAt,omitempty"`
}

// Validate checks an AgentSubscription's fields. Empty ID is allowed here —
// the API layer assigns IDs on create.
func (s *AgentSubscription) Validate() error {
	if s == nil {
		return errors.New("subscription is nil")
	}
	if s.Event != SubscriptionEventMessagePosted {
		return fmt.Errorf("unsupported subscription event %q (only %q is supported)", s.Event, SubscriptionEventMessagePosted)
	}
	if !isValidID(s.ScopeChannelID) {
		return errors.New("scopeChannelID is required")
	}
	if !isValidID(s.TargetChannelID) {
		return errors.New("targetChannelID is required")
	}
	if err := validateTriggerPrompt(s.Prompt); err != nil {
		return err
	}
	return validateAllowedTools(s.AllowedTools)
}

// Validate checks an AgentSchedule's fields.
func (s *AgentSchedule) Validate() error {
	if s == nil {
		return errors.New("schedule is nil")
	}
	if s.IntervalSeconds < MinScheduleIntervalSeconds {
		return fmt.Errorf("intervalSeconds must be at least %d (got %d)", MinScheduleIntervalSeconds, s.IntervalSeconds)
	}
	if !isValidID(s.TargetChannelID) {
		return errors.New("targetChannelID is required")
	}
	if err := validateTriggerPrompt(s.Prompt); err != nil {
		return err
	}
	return validateAllowedTools(s.AllowedTools)
}

// TriggerVars is the fixed, reflection-free set of fields exposed to trigger
// prompt templates. The dispatcher fills in whichever fields apply to the
// firing trigger; absent fields remain zero-valued and should be treated as
// empty by the template. Keeping this closed guarantees prompt-injection
// attempts cannot coerce arbitrary fields out of an inbound model.Post.
type TriggerVars struct {
	// Post-fields populated on subscription fires.
	UserID    string
	Username  string
	ChannelID string
	PostID    string
	Message   string

	// Common to all triggers.
	TargetChannelID string
	Now             string // RFC3339 UTC stamp at dispatch time
}

func isValidID(id string) bool {
	// Mattermost model IDs are 26 characters; accept that exact shape.
	// This is deliberately strict — a truncated or pasted URL should fail here.
	if len(id) != 26 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func validateTriggerPrompt(prompt string) error {
	if prompt == "" {
		return errors.New("prompt is required")
	}
	if utf8.RuneCountInString(prompt) > MaxTriggerPromptRunes {
		return fmt.Errorf("prompt exceeds maximum length of %d characters", MaxTriggerPromptRunes)
	}
	return nil
}

func validateAllowedTools(tools []string) error {
	for _, name := range tools {
		if name == "" {
			return errors.New("allowedTools contains an empty name")
		}
		if slices.Contains(ReservedBoundToolNames, name) {
			return fmt.Errorf("tool %q is not allowed in scoped runs", name)
		}
	}
	return nil
}
