// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- in-package stubs ---

type decisionCall struct {
	userID       string
	resourceType string
	resourceID   string
	action       string
}

// stubDecisionClient returns a fixed outcome/error, optionally overridden per
// resource type, and records every call.
type stubDecisionClient struct {
	outcome    Outcome
	err        error
	perType    map[string]Outcome // resourceType → outcome override
	perTypeErr map[string]error   // resourceType → error override
	calls      []decisionCall
}

func (s *stubDecisionClient) EvaluateAccessRequest(_ context.Context, userID, resourceType, resourceID, action string) (Outcome, error) {
	s.calls = append(s.calls, decisionCall{userID: userID, resourceType: resourceType, resourceID: resourceID, action: action})
	if err, ok := s.perTypeErr[resourceType]; ok {
		return "", err
	}
	if outcome, ok := s.perType[resourceType]; ok {
		return outcome, nil
	}
	if s.err != nil {
		return "", s.err
	}
	return s.outcome, s.err
}

// stubPolicyIndex is a map-backed PolicyIndex with injectable errors.
// Successful Add/Remove also update `has` so marker state stays observable.
type stubPolicyIndex struct {
	has       map[string]bool // key: resourceType + "/" + resourceID
	hasErr    error
	addErr    error
	removeErr error
	added     []string
	removed   []string
}

func indexKey(resourceType, resourceID string) string { return resourceType + "/" + resourceID }

func (s *stubPolicyIndex) Has(resourceType, resourceID string) (bool, error) {
	if s.hasErr != nil {
		return false, s.hasErr
	}
	return s.has[indexKey(resourceType, resourceID)], nil
}

func (s *stubPolicyIndex) Add(resourceType, resourceID string) error {
	if s.addErr != nil {
		return s.addErr
	}
	s.added = append(s.added, indexKey(resourceType, resourceID))
	if s.has == nil {
		s.has = map[string]bool{}
	}
	s.has[indexKey(resourceType, resourceID)] = true
	return nil
}

func (s *stubPolicyIndex) Remove(resourceType, resourceID string) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	s.removed = append(s.removed, indexKey(resourceType, resourceID))
	delete(s.has, indexKey(resourceType, resourceID))
	return nil
}

func newTestChecker() *Checker {
	return New(PassthroughClient{}, nil, EmptyPolicyIndex{}, NoMCPServerIDs, nil, nil)
}

// --- WS-C passthrough pins (the no_policy rows must keep behaving like this) ---

func TestPassthroughClientEvaluateAccessRequest(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
	}{
		{name: "agent resource", resourceType: ResourceTypeAgent},
		{name: "service resource", resourceType: ResourceTypeService},
		{name: "mcp resource", resourceType: ResourceTypeMCP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := PassthroughClient{}.EvaluateAccessRequest(context.Background(), "userid", tt.resourceType, "resourceid", ActionUse)
			require.NoError(t, err)
			assert.Equal(t, OutcomeNoPolicy, outcome)
		})
	}
}

func TestCheckerCanUseAgentPassthrough(t *testing.T) {
	legacyErr := errors.New("legacy restriction")

	tests := []struct {
		name        string
		legacyCheck func() error
		wantErr     error
	}{
		{name: "nil legacy check allows", legacyCheck: nil, wantErr: nil},
		{name: "passing legacy check allows", legacyCheck: func() error { return nil }, wantErr: nil},
		{name: "failing legacy check returns same error", legacyCheck: func() error { return legacyErr }, wantErr: legacyErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestChecker()
			err := c.CanUseAgent(context.Background(), "userid", &llm.BotConfig{ID: "agentid"}, tt.legacyCheck)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckerPassthroughHelpersAllow(t *testing.T) {
	c := newTestChecker()

	tests := []struct {
		name  string
		check func() error
	}{
		{name: "CanUseService", check: func() error { return c.CanUseService(context.Background(), "userid", "serviceid") }},
		{name: "CanUseMCPServer", check: func() error { return c.CanUseMCPServer(context.Background(), "userid", "serverid") }},
		{name: "ValidateAgentWrite", check: func() error {
			return c.ValidateAgentWrite(context.Background(), "userid", &llm.BotConfig{ID: "agentid"}, nil)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, tt.check())
		})
	}
}

func TestEmptyPolicyIndexHas(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		resourceID   string
	}{
		{name: "agent", resourceType: ResourceTypeAgent, resourceID: "agentid"},
		{name: "service", resourceType: ResourceTypeService, resourceID: "serviceid"},
		{name: "mcp", resourceType: ResourceTypeMCP, resourceID: "serverid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			has, err := EmptyPolicyIndex{}.Has(tt.resourceType, tt.resourceID)
			assert.NoError(t, err)
			assert.False(t, has)
		})
	}
}

// --- §9.2 agent decision table ---

func TestCanUseAgentDecisionTable(t *testing.T) {
	legacyErr := errors.New("legacy restriction")
	evalErr := errors.New("pdp exploded")
	indexErr := errors.New("kv unreadable")

	// legacy check variants shared by the table rows
	const (
		legacyNil = iota
		legacyPass
		legacyFail
	)

	tests := []struct {
		name            string
		outcome         Outcome
		evalErr         error
		attributeBased  bool
		legacy          int
		indexHas        bool
		indexErr        error
		wantDenied      bool // errors.Is(err, ErrAccessDenied)
		wantLegacyErr   bool // the exact legacy error is returned
		wantLegacyCalls int  // how many times legacyCheck must run
	}{
		// attribute-based mode: legacy check must NEVER run
		{name: "attr allow", outcome: OutcomeAllow, attributeBased: true, legacy: legacyFail},
		{name: "attr deny", outcome: OutcomeDeny, attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr no_policy fails open", outcome: OutcomeNoPolicy, attributeBased: true, legacy: legacyFail},
		{name: "attr unavailable fails closed", outcome: OutcomeUnavailable, attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr eval error fails closed", evalErr: evalErr, attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr unknown outcome fails closed", outcome: Outcome("future_value"), attributeBased: true, legacy: legacyPass, wantDenied: true},

		// legacy modes: allow/no_policy defer to the legacy check
		{name: "legacy allow runs legacy pass", outcome: OutcomeAllow, legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy allow runs legacy fail", outcome: OutcomeAllow, legacy: legacyFail, wantLegacyErr: true, wantLegacyCalls: 1},
		{name: "legacy allow nil legacy", outcome: OutcomeAllow, legacy: legacyNil},
		{name: "legacy no_policy runs legacy pass", outcome: OutcomeNoPolicy, legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy no_policy runs legacy fail", outcome: OutcomeNoPolicy, legacy: legacyFail, wantLegacyErr: true, wantLegacyCalls: 1},

		// deny always denies, legacy never consulted
		{name: "legacy deny", outcome: OutcomeDeny, legacy: legacyPass, wantDenied: true},

		// unavailable/error: index marker decides
		{name: "legacy unavailable no marker runs legacy", outcome: OutcomeUnavailable, legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy unavailable no marker legacy fail", outcome: OutcomeUnavailable, legacy: legacyFail, wantLegacyErr: true, wantLegacyCalls: 1},
		{name: "legacy unavailable with marker denies", outcome: OutcomeUnavailable, legacy: legacyPass, indexHas: true, wantDenied: true},
		{name: "legacy eval error no marker runs legacy", evalErr: evalErr, legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy eval error with marker denies", evalErr: evalErr, legacy: legacyPass, indexHas: true, wantDenied: true},

		// index read errors fail closed (DECISION 7)
		{name: "legacy unavailable index error denies", outcome: OutcomeUnavailable, legacy: legacyPass, indexErr: indexErr, wantDenied: true},
		{name: "legacy eval error index error denies", evalErr: evalErr, legacy: legacyPass, indexErr: indexErr, wantDenied: true},

		// unknown outcome behaves like the unavailable row
		{name: "legacy unknown outcome no marker runs legacy", outcome: Outcome("future_value"), legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy unknown outcome with marker denies", outcome: Outcome("future_value"), legacy: legacyPass, indexHas: true, wantDenied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentID := model.NewId()
			userID := model.NewId()

			client := &stubDecisionClient{outcome: tt.outcome, err: tt.evalErr}
			index := &stubPolicyIndex{has: map[string]bool{}, hasErr: tt.indexErr}
			if tt.indexHas {
				index.has[indexKey(ResourceTypeAgent, agentID)] = true
			}
			c := New(client, nil, index, NoMCPServerIDs, nil, nil)

			cfg := &llm.BotConfig{ID: agentID}
			if tt.attributeBased {
				cfg.UserAccessLevel = llm.UserAccessLevelAttributeBased
			}

			legacyCalls := 0
			var legacyCheck func() error
			switch tt.legacy {
			case legacyPass:
				legacyCheck = func() error { legacyCalls++; return nil }
			case legacyFail:
				legacyCheck = func() error { legacyCalls++; return legacyErr }
			}

			err := c.CanUseAgent(context.Background(), userID, cfg, legacyCheck)

			switch {
			case tt.wantDenied:
				assert.ErrorIs(t, err, ErrAccessDenied)
			case tt.wantLegacyErr:
				assert.ErrorIs(t, err, legacyErr)
				assert.NotErrorIs(t, err, ErrAccessDenied)
			default:
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantLegacyCalls, legacyCalls, "legacy check invocation count")
			if tt.attributeBased {
				assert.Zero(t, legacyCalls, "legacy check must never run in attribute-based mode")
			}
			require.Len(t, client.calls, 1)
			assert.Equal(t, decisionCall{userID: userID, resourceType: ResourceTypeAgent, resourceID: agentID, action: ActionUse}, client.calls[0])
		})
	}
}

// --- §9.2 mode-less service/MCP decision table ---

func TestCanUseServiceAndMCPServerDecisionTable(t *testing.T) {
	evalErr := errors.New("pdp exploded")
	indexErr := errors.New("kv unreadable")

	rows := []struct {
		name       string
		outcome    Outcome
		evalErr    error
		indexHas   bool
		indexErr   error
		wantDenied bool
	}{
		{name: "allow", outcome: OutcomeAllow},
		{name: "no_policy", outcome: OutcomeNoPolicy},
		{name: "deny", outcome: OutcomeDeny, wantDenied: true},
		{name: "unavailable no marker allows", outcome: OutcomeUnavailable},
		{name: "unavailable with marker denies", outcome: OutcomeUnavailable, indexHas: true, wantDenied: true},
		{name: "unavailable index error denies", outcome: OutcomeUnavailable, indexErr: indexErr, wantDenied: true},
		{name: "eval error no marker allows", evalErr: evalErr},
		{name: "eval error with marker denies", evalErr: evalErr, indexHas: true, wantDenied: true},
		{name: "eval error index error denies", evalErr: evalErr, indexErr: indexErr, wantDenied: true},
		{name: "unknown outcome no marker allows", outcome: Outcome("future_value")},
		{name: "unknown outcome with marker denies", outcome: Outcome("future_value"), indexHas: true, wantDenied: true},
	}

	checks := []struct {
		name         string
		resourceType string
		invoke       func(c *Checker, userID, resourceID string) error
	}{
		{name: "CanUseService", resourceType: ResourceTypeService, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseService(context.Background(), userID, resourceID)
		}},
		{name: "CanUseMCPServer", resourceType: ResourceTypeMCP, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseMCPServer(context.Background(), userID, resourceID)
		}},
	}

	for _, check := range checks {
		for _, tt := range rows {
			t.Run(check.name+" "+tt.name, func(t *testing.T) {
				resourceID := model.NewId()
				userID := model.NewId()

				client := &stubDecisionClient{outcome: tt.outcome, err: tt.evalErr}
				index := &stubPolicyIndex{has: map[string]bool{}, hasErr: tt.indexErr}
				if tt.indexHas {
					index.has[indexKey(check.resourceType, resourceID)] = true
				}
				c := New(client, nil, index, NoMCPServerIDs, nil, nil)

				err := check.invoke(c, userID, resourceID)
				if tt.wantDenied {
					assert.ErrorIs(t, err, ErrAccessDenied)
				} else {
					assert.NoError(t, err)
				}
				require.Len(t, client.calls, 1)
				assert.Equal(t, decisionCall{userID: userID, resourceType: check.resourceType, resourceID: resourceID, action: ActionUse}, client.calls[0])
			})
		}
	}
}

// --- DECISION 5: invalid IDs are no_policy, never PDP calls ---

func TestEvaluateInvalidIDsAreNoPolicy(t *testing.T) {
	legacyErr := errors.New("legacy restriction")

	tests := []struct {
		name       string
		userID     string
		resourceID string
	}{
		{name: "uuid resource id", userID: model.NewId(), resourceID: "550e8400-e29b-41d4-a716-446655440000"},
		{name: "empty resource id", userID: model.NewId(), resourceID: ""},
		{name: "invalid user id", userID: "not-a-user", resourceID: model.NewId()},
		{name: "both invalid", userID: "", resourceID: "config-bot"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Deny-everything client: if it were consulted these would all fail.
			client := &stubDecisionClient{outcome: OutcomeDeny}
			c := New(client, nil, EmptyPolicyIndex{}, NoMCPServerIDs, nil, nil)

			// Agent path: falls back to legacy behavior.
			legacyCalls := 0
			err := c.CanUseAgent(context.Background(), tt.userID, &llm.BotConfig{ID: tt.resourceID}, func() error { legacyCalls++; return legacyErr })
			assert.ErrorIs(t, err, legacyErr)
			assert.Equal(t, 1, legacyCalls)

			// Mode-less paths: allow.
			assert.NoError(t, c.CanUseService(context.Background(), tt.userID, tt.resourceID))
			assert.NoError(t, c.CanUseMCPServer(context.Background(), tt.userID, tt.resourceID))

			assert.Empty(t, client.calls, "decision client must never be called for invalid IDs")
		})
	}
}

// --- IsAvailable probe + TTL cache ---

func TestIsAvailable(t *testing.T) {
	tests := []struct {
		name    string
		outcome Outcome
		err     error
		want    bool
	}{
		{name: "no_policy means available", outcome: OutcomeNoPolicy, want: true},
		{name: "allow means available", outcome: OutcomeAllow, want: true},
		{name: "unavailable means unavailable", outcome: OutcomeUnavailable, want: false},
		{name: "error means unavailable", err: errors.New("pdp exploded"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &stubDecisionClient{outcome: tt.outcome, err: tt.err}
			c := New(client, nil, EmptyPolicyIndex{}, NoMCPServerIDs, nil, nil)
			userID := model.NewId()

			assert.Equal(t, tt.want, c.IsAvailable(context.Background(), userID))
			require.Len(t, client.calls, 1)
			assert.Equal(t, ResourceTypeAgent, client.calls[0].resourceType)

			// Second call within the TTL is served from cache.
			assert.Equal(t, tt.want, c.IsAvailable(context.Background(), userID))
			assert.Len(t, client.calls, 1, "cached availability must not re-hit the client")

			// Expiring the cache re-probes.
			c.availabilityChecked = c.availabilityChecked.Add(-2 * availabilityCacheTTL)
			assert.Equal(t, tt.want, c.IsAvailable(context.Background(), userID))
			assert.Len(t, client.calls, 2)
		})
	}
}

// --- ValidateAgentWrite (contract §9.1 + DECISION 9) ---

func TestValidateAgentWrite(t *testing.T) {
	serviceID := model.NewId()
	prevServiceID := model.NewId()
	mcpServerID := model.NewId()
	otherMCPServerID := model.NewId()

	const (
		allowedOrigin = "https://mcp-allowed.example.com"
		deniedOrigin  = "https://mcp-denied.example.com"
		unknownOrigin = "embedded://mattermost"
	)

	resolver := func() map[string]string {
		return map[string]string{
			allowedOrigin: otherMCPServerID,
			deniedOrigin:  mcpServerID,
		}
	}

	tools := func(origins ...string) []llm.EnabledMCPTool {
		var out []llm.EnabledMCPTool
		for _, o := range origins {
			out = append(out, llm.EnabledMCPTool{ServerOrigin: o, ToolName: "some_tool"})
		}
		return out
	}

	tests := []struct {
		name          string
		perType       map[string]Outcome
		cfg           *llm.BotConfig
		prev          *llm.BotConfig
		emptyResolver bool
		wantErr       error
		wantErrText   string
		wantTypes     []string // resource types the client must have been called with, in order
	}{
		{
			name:      "create attribute-based while unavailable",
			perType:   map[string]Outcome{ResourceTypeAgent: OutcomeUnavailable},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID, UserAccessLevel: llm.UserAccessLevelAttributeBased},
			wantErr:   ErrABACUnavailable,
			wantTypes: []string{ResourceTypeAgent}, // availability probe only
		},
		{
			name:      "create attribute-based while available",
			perType:   map[string]Outcome{ResourceTypeAgent: OutcomeNoPolicy, ResourceTypeService: OutcomeAllow},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID, UserAccessLevel: llm.UserAccessLevelAttributeBased},
			wantTypes: []string{ResourceTypeAgent, ResourceTypeService},
		},
		{
			name:        "create with denied service",
			perType:     map[string]Outcome{ResourceTypeService: OutcomeDeny},
			cfg:         &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			wantErr:     ErrAccessDenied,
			wantErrText: "service",
			wantTypes:   []string{ResourceTypeService},
		},
		{
			name:      "update with unchanged denied service skips the service check",
			perType:   map[string]Outcome{ResourceTypeService: OutcomeDeny},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			prev:      &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			wantTypes: nil,
		},
		{
			name:      "update with changed service checks the new one",
			perType:   map[string]Outcome{ResourceTypeService: OutcomeDeny},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			prev:      &llm.BotConfig{ID: model.NewId(), ServiceID: prevServiceID},
			wantErr:   ErrAccessDenied,
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:    "create with denied mcp origin names the origin",
			perType: map[string]Outcome{ResourceTypeService: OutcomeAllow, ResourceTypeMCP: OutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin),
			},
			wantErr:     ErrAccessDenied,
			wantErrText: deniedOrigin,
			wantTypes:   []string{ResourceTypeService, ResourceTypeMCP},
		},
		{
			name:    "update skips pre-existing mcp origins",
			perType: map[string]Outcome{ResourceTypeMCP: OutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin, deniedOrigin+"/"), // normalization dedupes the slash variant
			},
			prev: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin),
			},
			wantTypes: nil,
		},
		{
			name:    "update checks only newly added mcp origins",
			perType: map[string]Outcome{ResourceTypeMCP: OutcomeAllow},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin, allowedOrigin),
			},
			prev: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin),
			},
			wantTypes: []string{ResourceTypeMCP},
		},
		{
			name:    "auto-enable-new-mcp-tools skips mcp validation",
			perType: map[string]Outcome{ResourceTypeService: OutcomeAllow, ResourceTypeMCP: OutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				AutoEnableNewMCPTools: true,
				EnabledMCPTools:       tools(deniedOrigin),
			},
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:    "empty resolver leaves origins non-addressable",
			perType: map[string]Outcome{ResourceTypeService: OutcomeAllow, ResourceTypeMCP: OutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin),
			},
			emptyResolver: true,
			wantTypes:     []string{ResourceTypeService},
		},
		{
			name:    "unresolvable origins are not policy-addressable",
			perType: map[string]Outcome{ResourceTypeService: OutcomeAllow, ResourceTypeMCP: OutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(unknownOrigin),
			},
			wantTypes: []string{ResourceTypeService},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &stubDecisionClient{outcome: OutcomeNoPolicy, perType: tt.perType}
			idsByOrigin := resolver
			if tt.emptyResolver {
				idsByOrigin = NoMCPServerIDs
			}
			c := New(client, nil, EmptyPolicyIndex{}, idsByOrigin, nil, nil)

			err := c.ValidateAgentWrite(context.Background(), model.NewId(), tt.cfg, tt.prev)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				if tt.wantErrText != "" {
					assert.Contains(t, err.Error(), tt.wantErrText)
				}
			} else {
				assert.NoError(t, err)
			}

			var gotTypes []string
			for _, call := range client.calls {
				gotTypes = append(gotTypes, call.resourceType)
			}
			assert.Equal(t, tt.wantTypes, gotTypes, "decision calls by resource type")
		})
	}
}

func TestNewPanicsWithoutMCPServerIDResolver(t *testing.T) {
	assert.Panics(t, func() {
		New(PassthroughClient{}, nil, EmptyPolicyIndex{}, nil, nil, nil)
	}, "a nil MCP server ID resolver is a wiring bug and must fail fast")
}
