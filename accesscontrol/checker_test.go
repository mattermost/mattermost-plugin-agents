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

// stubDecisionClient returns a fixed decision/error, optionally overridden per
// resource type, and records every call. A nil decision with a nil error
// exercises the DecisionClient contract guard.
type stubDecisionClient struct {
	decision   *model.AccessDecision
	err        error
	perType    map[string]*model.AccessDecision // resourceType → decision override
	perTypeErr map[string]error                 // resourceType → error override
	calls      []decisionCall
}

func (s *stubDecisionClient) EvaluateAccessRequest(_ context.Context, userID, resourceType, resourceID, action string) (*model.AccessDecision, error) {
	s.calls = append(s.calls, decisionCall{userID: userID, resourceType: resourceType, resourceID: resourceID, action: action})
	if err, ok := s.perTypeErr[resourceType]; ok {
		return nil, err
	}
	if decision, ok := s.perType[resourceType]; ok {
		return decision, nil
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.decision, nil
}

// Decision shorthands for the tables below.
func allowDecision() *model.AccessDecision { return &model.AccessDecision{Decision: true} }
func denyDecision() *model.AccessDecision  { return &model.AccessDecision{Decision: false} }

func noPolicyDecision() *model.AccessDecision {
	d := model.NewNoPolicyAccessDecision()
	return &d
}

// contradictoryDecision is a deny carrying the no_policy reason. The PDP should
// never emit one; if the checkers read the reason without the boolean they
// would fail open on it.
func contradictoryDecision() *model.AccessDecision {
	return &model.AccessDecision{
		Decision: false,
		Context:  map[string]any{model.AccessDecisionContextKeyReason: string(model.AccessDecisionReasonNoPolicy)},
	}
}

func newTestChecker() *Checker {
	return New(PassthroughClient{}, nil, NoMCPServerIDs, nil)
}

// The checkers deny outright on a user ID no policy can be evaluated against, so
// tests that mean to exercise anything past that gate need a well-formed one.
// Resource IDs are free to be arbitrary strings: callers choose them.
var testUserID = model.NewId()

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
			decision, err := PassthroughClient{}.EvaluateAccessRequest(context.Background(), testUserID, tt.resourceType, "resourceid", ActionUse)
			require.NoError(t, err)
			require.NotNil(t, decision)
			assert.True(t, decision.IsNoPolicy())
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
			err := c.CanUseAgent(context.Background(), testUserID, &llm.BotConfig{ID: "agentid"}, tt.legacyCheck)
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
		{name: "CanUseService", check: func() error { return c.CanUseService(context.Background(), testUserID, "serviceid") }},
		{name: "CanUseMCPServer", check: func() error { return c.CanUseMCPServer(context.Background(), testUserID, "serverid") }},
		{name: "ValidateAgentWrite", check: func() error {
			return c.ValidateAgentWrite(context.Background(), testUserID, &llm.BotConfig{ID: "agentid"}, nil)
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
		decision        *model.AccessDecision
		evalErr         error
		attributeBased  bool
		legacy          int
		wantDenied      bool // errors.Is(err, ErrAccessDenied)
		wantLegacyErr   bool // the exact legacy error is returned
		wantLegacyCalls int  // how many times legacyCheck must run
	}{
		// attribute-based mode: legacy check must NEVER run
		{name: "attr allow", decision: allowDecision(), attributeBased: true, legacy: legacyFail},
		{name: "attr deny", decision: denyDecision(), attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr no_policy fails open", decision: noPolicyDecision(), attributeBased: true, legacy: legacyFail},
		{name: "attr contradictory deny fails closed", decision: contradictoryDecision(), attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr eval error fails closed", evalErr: evalErr, attributeBased: true, legacy: legacyPass, wantDenied: true},
		{name: "attr missing decision fails closed", attributeBased: true, legacy: legacyPass, wantDenied: true},

		// legacy modes: any allow defers to the legacy check
		{name: "legacy allow runs legacy pass", decision: allowDecision(), legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy allow runs legacy fail", decision: allowDecision(), legacy: legacyFail, wantLegacyErr: true, wantLegacyCalls: 1},
		{name: "legacy allow nil legacy", decision: allowDecision(), legacy: legacyNil},

		{name: "legacy no_policy runs legacy pass", decision: noPolicyDecision(), legacy: legacyPass, wantLegacyCalls: 1},
		{name: "legacy no_policy runs legacy fail", decision: noPolicyDecision(), legacy: legacyFail, wantLegacyErr: true, wantLegacyCalls: 1},

		// a deny, or any failure to obtain a decision, fails closed in every mode
		{name: "legacy deny", decision: denyDecision(), legacy: legacyPass, wantDenied: true},
		{name: "legacy contradictory deny denies despite passing legacy check", decision: contradictoryDecision(), legacy: legacyPass, wantDenied: true},
		{name: "legacy eval error denies despite passing legacy check", evalErr: evalErr, legacy: legacyPass, wantDenied: true},
		{name: "legacy eval error denies with nil legacy check", evalErr: evalErr, legacy: legacyNil, wantDenied: true},
		{name: "legacy missing decision denies", legacy: legacyPass, wantDenied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentID := model.NewId()
			userID := model.NewId()

			client := &stubDecisionClient{decision: tt.decision, err: tt.evalErr}
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
		decision   *model.AccessDecision
		evalErr    error
		wantDenied bool
	}{
		{name: "allow", decision: allowDecision()},
		{name: "no_policy", decision: noPolicyDecision()},
		{name: "deny", decision: denyDecision(), wantDenied: true},
		{name: "contradictory deny denies", decision: contradictoryDecision(), wantDenied: true},
		{name: "eval error denies", evalErr: evalErr, wantDenied: true},
		{name: "missing decision denies", wantDenied: true},
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

				client := &stubDecisionClient{decision: tt.decision, err: tt.evalErr}
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

// --- non-addressable resource IDs are no_policy, never PDP calls ---

// Resource IDs the caller chooses (config-file bot IDs, service and MCP server
// IDs) need not be 26-char platform IDs. No policy can be stored against one, so
// the resource is ungoverned and the caller's own default applies.
func TestEvaluateNonAddressableResourceIDsAreNoPolicy(t *testing.T) {
	legacyErr := errors.New("legacy restriction")

	tests := []struct {
		name       string
		resourceID string
	}{
		{name: "uuid resource id", resourceID: "550e8400-e29b-41d4-a716-446655440000"},
		{name: "empty resource id", resourceID: ""},
		{name: "config file bot id", resourceID: "config-bot"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Deny-everything client: if it were consulted these would all fail.
			client := &stubDecisionClient{decision: denyDecision()}
			c := New(client, nil, NoMCPServerIDs, nil)
			userID := model.NewId()

			// Agent path: falls back to legacy behavior.
			legacyCalls := 0
			err := c.CanUseAgent(context.Background(), userID, &llm.BotConfig{ID: tt.resourceID}, func() error { legacyCalls++; return legacyErr })
			assert.ErrorIs(t, err, legacyErr)
			assert.Equal(t, 1, legacyCalls)

			// Mode-less paths: allow.
			assert.NoError(t, c.CanUseService(context.Background(), userID, tt.resourceID))
			assert.NoError(t, c.CanUseMCPServer(context.Background(), userID, tt.resourceID))

			assert.Empty(t, client.calls, "decision client must never be called for a non-addressable resource ID")
		})
	}
}

// --- non-addressable user IDs deny, never PDP calls ---

// A user ID that cannot name a user is a caller bug with no legitimate case, so
// it denies rather than reporting the resource ungoverned. Treating it as
// no_policy would fail attribute-based agents open.
func TestEvaluateInvalidUserIDDenies(t *testing.T) {
	userIDs := []struct {
		name   string
		userID string
	}{
		{name: "empty", userID: ""},
		{name: "not an id", userID: "not-a-user"},
		{name: "uuid", userID: "550e8400-e29b-41d4-a716-446655440000"},
	}

	// Attribute-based agents are the ones that fail open on a wrong no_policy;
	// the legacy modes must deny too rather than run their legacy check.
	agentModes := []llm.UserAccessLevel{
		llm.UserAccessLevelAttributeBased,
		llm.UserAccessLevelAll,
		llm.UserAccessLevelAllow,
		llm.UserAccessLevelBlock,
	}

	for _, tt := range userIDs {
		t.Run(tt.name, func(t *testing.T) {
			// Allow-everything client: only the short-circuit can deny here.
			client := &stubDecisionClient{decision: allowDecision()}
			c := New(client, nil, NoMCPServerIDs, nil)
			resourceID := model.NewId()

			for _, mode := range agentModes {
				legacyCalls := 0
				err := c.CanUseAgent(context.Background(), tt.userID, &llm.BotConfig{ID: resourceID, UserAccessLevel: mode}, func() error { legacyCalls++; return nil })
				assert.ErrorIs(t, err, ErrAccessDenied, "agent mode %d must deny", mode)
				assert.Zero(t, legacyCalls, "agent mode %d must not reach its legacy check", mode)
			}

			assert.ErrorIs(t, c.CanUseService(context.Background(), tt.userID, resourceID), ErrAccessDenied)
			assert.ErrorIs(t, c.CanUseMCPServer(context.Background(), tt.userID, resourceID), ErrAccessDenied)

			assert.Empty(t, client.calls, "decision client must never be called for a non-addressable user ID")
		})
	}
}

// --- IsAvailable probe (CEL readiness path) + TTL cache ---

// The probe must report available in exactly one server state: enterprise,
// licensed for Enterprise Advanced, with ABAC enabled. Each row below is what
// GetAccessControlVisualAST returns in one state, read off the platform:
// App.ExpressionToVisualAST answers 501 when the access-control service is
// absent, the enterprise service's readiness gate answers 501 unlicensed and 406
// with ABAC disabled, and past that gate a "true" expression is special-cased to
// an empty condition set — so only the ready state yields a non-nil AST.
func TestIsAvailable(t *testing.T) {
	noService := model.NewAppError("ExpressionToVisualAST", "app.pap.expression_to_visual_ast.app_error", nil, "Policy Administration Point is not initialized", http.StatusNotImplemented)
	unlicensed := model.NewAppError("Init", "app.pap.init.app_error", nil, "enterprise advanced license required", http.StatusNotImplemented)
	abacDisabled := model.NewAppError("isReady", "app.pap.is_ready.app_error", nil, "access control is disabled", http.StatusNotAcceptable)
	compileErr := model.NewAppError("ExpressionToVisualAST", "app.pap.expression_to_visual_ast.app_error", nil, "boom", http.StatusBadRequest)

	tests := []struct {
		name   string
		ast    *model.VisualExpression
		appErr *model.AppError
		want   bool
	}{
		{name: "open core: no access control service", appErr: noService, want: false},
		{name: "enterprise, unlicensed", appErr: unlicensed, want: false},
		{name: "enterprise, licensed, ABAC disabled", appErr: abacDisabled, want: false},
		{name: "ready", ast: &model.VisualExpression{Conditions: []model.Condition{}}, want: true},
		{name: "unexpected compile failure means unavailable", appErr: compileErr, want: false},
		{name: "transport failure (nil, nil) means unavailable", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			api.On("GetAccessControlVisualAST", mock.AnythingOfType("string"), ResourceTypeAgent, availabilityProbeExpression).Return(tt.ast, tt.appErr).Twice()

			c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)
			actingUserID := model.NewId()

			assert.Equal(t, tt.want, c.IsAvailable(context.Background(), actingUserID))
			api.AssertNumberOfCalls(t, "GetAccessControlVisualAST", 1)

			// Second call within the TTL is served from cache.
			assert.Equal(t, tt.want, c.IsAvailable(context.Background(), actingUserID))
			api.AssertNumberOfCalls(t, "GetAccessControlVisualAST", 1)

			// Expiring the cache re-probes.
			c.availabilityChecked = c.availabilityChecked.Add(-2 * availabilityCacheTTL)
			assert.Equal(t, tt.want, c.IsAvailable(context.Background(), actingUserID))
			api.AssertNumberOfCalls(t, "GetAccessControlVisualAST", 2)
		})
	}
}

// A policy read cannot stand in for the probe: the platform answers an absent
// row with its uniform 404 before it consults the license, so an unlicensed
// enterprise server would look available.
func TestIsAvailableDoesNotProbeAPolicyRead(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("GetAccessControlVisualAST", mock.AnythingOfType("string"), ResourceTypeAgent, availabilityProbeExpression).
		Return(nil, model.NewAppError("Init", "app.pap.init.app_error", nil, "enterprise advanced license required", http.StatusNotImplemented)).Once()

	c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)

	assert.False(t, c.IsAvailable(context.Background(), model.NewId()))
	api.AssertNotCalled(t, "GetAccessControlPolicy", mock.Anything)
}

func TestIsAvailableWithoutPluginAPI(t *testing.T) {
	c := New(PassthroughClient{}, nil, NoMCPServerIDs, nil)
	assert.False(t, c.IsAvailable(context.Background(), model.NewId()), "a checker without a plugin API has no PAP to probe")
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

	// Availability-probe variants for the attribute-based rows: the probe is the
	// CEL readiness path (GetAccessControlVisualAST for a trivial expression), so
	// those rows inject a plugintest mock; the rest keep papi = nil (probe never
	// runs).
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
		perType       map[string]*model.AccessDecision
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
			perType:   map[string]*model.AccessDecision{ResourceTypeService: allowDecision()},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID, UserAccessLevel: llm.UserAccessLevelAttributeBased},
			probe:     probeAvailable,
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:        "create with denied service",
			perType:     map[string]*model.AccessDecision{ResourceTypeService: denyDecision()},
			cfg:         &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			wantErr:     ErrAccessDenied,
			wantErrText: "service",
			wantTypes:   []string{ResourceTypeService},
		},
		{
			name:      "update with unchanged denied service skips the service check",
			perType:   map[string]*model.AccessDecision{ResourceTypeService: denyDecision()},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			prev:      &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			wantTypes: nil,
		},
		{
			name:      "update with changed service checks the new one",
			perType:   map[string]*model.AccessDecision{ResourceTypeService: denyDecision()},
			cfg:       &llm.BotConfig{ID: model.NewId(), ServiceID: serviceID},
			prev:      &llm.BotConfig{ID: model.NewId(), ServiceID: prevServiceID},
			wantErr:   ErrAccessDenied,
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:    "create with denied mcp origin names the origin",
			perType: map[string]*model.AccessDecision{ResourceTypeService: allowDecision(), ResourceTypeMCP: denyDecision()},
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
			perType: map[string]*model.AccessDecision{ResourceTypeMCP: denyDecision()},
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
			perType: map[string]*model.AccessDecision{ResourceTypeMCP: allowDecision()},
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
			perType: map[string]*model.AccessDecision{ResourceTypeService: allowDecision(), ResourceTypeMCP: denyDecision()},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				AutoEnableNewMCPTools: true,
				EnabledMCPTools:       tools(deniedOrigin),
			},
			wantTypes: []string{ResourceTypeService},
		},
		{
			name:    "empty resolver leaves origins non-addressable",
			perType: map[string]*model.AccessDecision{ResourceTypeService: allowDecision(), ResourceTypeMCP: denyDecision()},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(deniedOrigin),
			},
			emptyResolver: true,
			wantTypes:     []string{ResourceTypeService},
		},
		{
			name:    "unresolvable origins are not policy-addressable",
			perType: map[string]*model.AccessDecision{ResourceTypeService: allowDecision(), ResourceTypeMCP: denyDecision()},
			cfg: &llm.BotConfig{
				ID: model.NewId(), ServiceID: serviceID,
				EnabledMCPTools: tools(unknownOrigin),
			},
			wantTypes: []string{ResourceTypeService},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &stubDecisionClient{decision: noPolicyDecision(), perType: tt.perType}
			idsByOrigin := resolver
			if tt.emptyResolver {
				idsByOrigin = NoMCPServerIDs
			}

			var c *Checker
			switch tt.probe {
			case probeAvailable, probeUnavailable:
				api := &plugintest.API{}
				defer api.AssertExpectations(t)
				var probeAST *model.VisualExpression
				var probeErr *model.AppError
				if tt.probe == probeAvailable {
					probeAST = &model.VisualExpression{Conditions: []model.Condition{}}
				} else {
					probeErr = model.NewAppError("Init", "app.pap.init.app_error", nil, "enterprise advanced license required", http.StatusNotImplemented)
				}
				api.On("GetAccessControlVisualAST", mock.AnythingOfType("string"), ResourceTypeAgent, availabilityProbeExpression).Return(probeAST, probeErr).Once()
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
