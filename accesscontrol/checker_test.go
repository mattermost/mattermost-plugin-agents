// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	outcome    model.AccessDecisionOutcome
	err        error
	perType    map[string]model.AccessDecisionOutcome // resourceType → outcome override
	perTypeErr map[string]error                       // resourceType → error override
	calls      []decisionCall
}

func (s *stubDecisionClient) EvaluateAccessRequest(_ context.Context, userID, resourceType, resourceID, action string) (model.AccessDecisionOutcome, error) {
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

func newTestChecker() *Checker {
	return New(PassthroughClient{}, nil, NoMCPServerIDs, nil)
}

// --- passthrough pins (the no_policy rows must keep behaving like this) ---

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
			assert.Equal(t, model.AccessDecisionOutcomeNoPolicy, outcome)
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

// --- agent decision table ---

func TestCanUseAgentDecisionTable(t *testing.T) {
	legacyErr := errors.New("legacy restriction")
	evalErr := errors.New("pdp exploded")

	// legacy check variants shared by the table rows
	const (
		legacyNil = iota
		legacyPass
		legacyFail
	)

	tests := []struct {
		name            string
		outcome         model.AccessDecisionOutcome
		evalErr         error
		attributeBased  bool
		legacy          int
		wantDenied      bool // errors.Is(err, ErrAccessDenied)
		wantLegacyErr   bool // the exact legacy error is returned
		wantLegacyCalls int  // how many times legacyCheck must run
	}{
		// attribute-based mode: legacy check must NEVER run
		{name: "attr allow", outcome: model.AccessDecisionOutcomeAllow, attributeBased: true, legacy: legacyFail},
		{name: "attr deny", outcome: model.AccessDecisionOutcomeDeny, attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr no_policy fails open", outcome: model.AccessDecisionOutcomeNoPolicy, attributeBased: true, legacy: legacyFail},
		{name: "attr unavailable fails closed", outcome: model.AccessDecisionOutcomeUnavailable, attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr eval error fails closed", evalErr: evalErr, attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr unknown outcome fails closed", outcome: model.AccessDecisionOutcome("future_value"), attributeBased: true, legacy: legacyPass, wantDenied: true},

		// legacy modes: allow/no_policy defer to the legacy check
		{name: "legacy allow runs legacy pass", outcome: model.AccessDecisionOutcomeAllow, legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy allow runs legacy fail", outcome: model.AccessDecisionOutcomeAllow, legacy: legacyFail, wantLegacyErr: true, wantLegacyCalls: 1},
		{name: "legacy allow nil legacy", outcome: model.AccessDecisionOutcomeAllow, legacy: legacyNil},

		{name: "legacy no_policy runs legacy pass", outcome: model.AccessDecisionOutcomeNoPolicy, legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy no_policy runs legacy fail", outcome: model.AccessDecisionOutcomeNoPolicy, legacy: legacyFail, wantLegacyErr: true, wantLegacyCalls: 1},

		// deny/unavailable/error/unknown fail closed in every mode
		{name: "legacy deny", outcome: model.AccessDecisionOutcomeDeny, legacy: legacyPass, wantDenied: true},
		{name: "legacy unavailable denies despite passing legacy check", outcome: model.AccessDecisionOutcomeUnavailable, legacy: legacyPass, wantDenied: true},
		{name: "legacy unavailable denies with nil legacy check", outcome: model.AccessDecisionOutcomeUnavailable, legacy: legacyNil, wantDenied: true},
		{name: "legacy eval error denies", evalErr: evalErr, legacy: legacyPass, wantDenied: true},
		{name: "legacy unknown outcome denies", outcome: model.AccessDecisionOutcome("future_value"), legacy: legacyPass, wantDenied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentID := model.NewId()
			userID := model.NewId()

			client := &stubDecisionClient{outcome: tt.outcome, err: tt.evalErr}
			c := New(client, nil, NoMCPServerIDs, nil)

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
			if tt.attributeBased || tt.wantDenied {
				assert.Zero(t, legacyCalls, "legacy check must never run on deny rows or in attribute-based mode")
			}
			require.Len(t, client.calls, 1)
			assert.Equal(t, decisionCall{userID: userID, resourceType: ResourceTypeAgent, resourceID: agentID, action: ActionUse}, client.calls[0])
		})
	}
}

// --- mode-less service/MCP decision table ---

func TestCanUseServiceAndMCPServerDecisionTable(t *testing.T) {
	evalErr := errors.New("pdp exploded")

	rows := []struct {
		name       string
		outcome    model.AccessDecisionOutcome
		evalErr    error
		wantDenied bool
	}{
		{name: "allow", outcome: model.AccessDecisionOutcomeAllow},
		{name: "no_policy", outcome: model.AccessDecisionOutcomeNoPolicy},
		{name: "deny", outcome: model.AccessDecisionOutcomeDeny, wantDenied: true},
		{name: "unavailable denies", outcome: model.AccessDecisionOutcomeUnavailable, wantDenied: true},
		{name: "eval error denies", evalErr: evalErr, wantDenied: true},
		{name: "unknown outcome denies", outcome: model.AccessDecisionOutcome("future_value"), wantDenied: true},
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
				c := New(client, nil, NoMCPServerIDs, nil)

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

// --- invalid IDs are no_policy, never PDP calls ---

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
			client := &stubDecisionClient{outcome: model.AccessDecisionOutcomeDeny}
			c := New(client, nil, NoMCPServerIDs, nil)

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

// --- IsAvailable probe (PAP read path) + TTL cache ---

func TestIsAvailable(t *testing.T) {
	notFound := model.NewAppError("GetAccessControlPolicy", "not found", nil, "", http.StatusNotFound)
	notImplemented := model.NewAppError("GetAccessControlPolicy", "abac unavailable", nil, "", http.StatusNotImplemented)
	serverErr := model.NewAppError("GetAccessControlPolicy", "boom", nil, "", http.StatusInternalServerError)

	tests := []struct {
		name   string
		policy *model.AccessControlPolicy
		appErr *model.AppError
		want   bool
	}{
		{name: "not found means available", appErr: notFound, want: true},
		{name: "unavailable-class error means unavailable", appErr: notImplemented, want: false},
		{name: "other errors mean unavailable", appErr: serverErr, want: false},
		{name: "existing policy means available", policy: &model.AccessControlPolicy{}, want: true},
		{name: "transport failure (nil, nil) means unavailable", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			// Fresh model.NewId() per probe: match any string argument.
			api.On("GetAccessControlPolicy", mock.AnythingOfType("string")).Return(tt.policy, tt.appErr).Twice()

			c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)

			assert.Equal(t, tt.want, c.IsAvailable(context.Background()))
			api.AssertNumberOfCalls(t, "GetAccessControlPolicy", 1)

			// Second call within the TTL is served from cache.
			assert.Equal(t, tt.want, c.IsAvailable(context.Background()))
			api.AssertNumberOfCalls(t, "GetAccessControlPolicy", 1)

			// Expiring the cache re-probes.
			c.availabilityChecked = c.availabilityChecked.Add(-2 * availabilityCacheTTL)
			assert.Equal(t, tt.want, c.IsAvailable(context.Background()))
			api.AssertNumberOfCalls(t, "GetAccessControlPolicy", 2)
		})
	}
}

func TestIsAvailableWithoutPluginAPI(t *testing.T) {
	c := New(PassthroughClient{}, nil, NoMCPServerIDs, nil)
	assert.False(t, c.IsAvailable(context.Background()), "a checker without a plugin API has no PAP to probe")
}

// --- ValidateAgentWrite ---

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

	// Availability-probe variants for the attribute-based rows: the probe is
	// the PAP read path (GetAccessControlPolicy on a fresh ID), so those rows
	// inject a plugintest mock; the rest keep papi = nil (probe never runs).
	const (
		probeNone = iota
		probeAvailable
		probeUnavailable
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
		perType       map[string]model.AccessDecisionOutcome
		cfg           *llm.BotConfig
		prev          *llm.BotConfig
		probe         int
		emptyResolver bool
		wantErr       error
		wantErrText   string
		wantTypes     []string // resource types the client must have been called with, in order
	}{
		{
			name:      "create attribute-based while unavailable",
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID, UserAccessLevel: llm.UserAccessLevelAttributeBased},
			probe:     probeUnavailable,
			wantErr:   ErrABACUnavailable,
			wantTypes: nil, // rejected before any decision call
		},
		{
			name:      "create attribute-based while available",
			perType:   map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeAllow},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID, UserAccessLevel: llm.UserAccessLevelAttributeBased},
			probe:     probeAvailable,
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:        "create with denied service",
			perType:     map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeDeny},
			cfg:         &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			wantErr:     ErrAccessDenied,
			wantErrText: "service",
			wantTypes:   []string{ResourceTypeService},
		},
		{
			name:      "update with unchanged denied service skips the service check",
			perType:   map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeDeny},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			prev:      &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			wantTypes: nil,
		},
		{
			name:      "update with changed service checks the new one",
			perType:   map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeDeny},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			prev:      &llm.BotConfig{ID: model.NewId(), ServiceID: prevServiceID},
			wantErr:   ErrAccessDenied,
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:    "create with denied mcp origin names the origin",
			perType: map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeAllow, ResourceTypeMCP: model.AccessDecisionOutcomeDeny},
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
			perType: map[string]model.AccessDecisionOutcome{ResourceTypeMCP: model.AccessDecisionOutcomeDeny},
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
			perType: map[string]model.AccessDecisionOutcome{ResourceTypeMCP: model.AccessDecisionOutcomeAllow},
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
			perType: map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeAllow, ResourceTypeMCP: model.AccessDecisionOutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				AutoEnableNewMCPTools: true,
				EnabledMCPTools:       tools(deniedOrigin),
			},
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:    "empty resolver leaves origins non-addressable",
			perType: map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeAllow, ResourceTypeMCP: model.AccessDecisionOutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin),
			},
			emptyResolver: true,
			wantTypes:     []string{ResourceTypeService},
		},
		{
			name:    "unresolvable origins are not policy-addressable",
			perType: map[string]model.AccessDecisionOutcome{ResourceTypeService: model.AccessDecisionOutcomeAllow, ResourceTypeMCP: model.AccessDecisionOutcomeDeny},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(unknownOrigin),
			},
			wantTypes: []string{ResourceTypeService},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &stubDecisionClient{outcome: model.AccessDecisionOutcomeNoPolicy, perType: tt.perType}
			idsByOrigin := resolver
			if tt.emptyResolver {
				idsByOrigin = NoMCPServerIDs
			}

			var c *Checker
			switch tt.probe {
			case probeAvailable, probeUnavailable:
				api := &plugintest.API{}
				defer api.AssertExpectations(t)
				probeErr := model.NewAppError("GetAccessControlPolicy", "not found", nil, "", http.StatusNotFound)
				if tt.probe == probeUnavailable {
					probeErr = model.NewAppError("GetAccessControlPolicy", "abac unavailable", nil, "", http.StatusNotImplemented)
				}
				api.On("GetAccessControlPolicy", mock.AnythingOfType("string")).Return(nil, probeErr).Once()
				c = New(client, api, idsByOrigin, nil)
			default:
				c = New(client, nil, idsByOrigin, nil)
			}

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
		New(PassthroughClient{}, nil, nil, nil)
	}, "a nil MCP server ID resolver is a wiring bug and must fail fast")
}
