// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ErrAccessDenied is wrapped by all deny returns of the decision tables.
var ErrAccessDenied = errors.New("access denied by policy")

// ErrABACUnavailable is returned by ValidateAgentWrite when an agent is being
// saved into attribute-based mode while ABAC is not available on the server.
var ErrABACUnavailable = errors.New("attribute-based access requires Attribute-Based Access Control to be licensed and enabled on this server")

// availabilityCacheTTL caps how often the availability probe hits the PAP.
const availabilityCacheTTL = 30 * time.Second

// Checker gates end-user access to agents, services, and external MCP servers,
// and hosts the PAP proxying (pap.go).
type Checker struct {
	client DecisionClient
	papi   plugin.API // PAP calls (pap.go); nil in decision-only tests
	log    Logger

	// legacyOnly marks pre-11.10 wiring (NewLegacyOnly): no decision or PAP
	// call is ever made, and attribute-based agents deny — policy existence
	// cannot be resolved, so the no_policy fail-open is unsafe.
	legacyOnly bool

	// mcpIDsByOrigin resolves external MCP server origins to stable IDs for
	// ValidateAgentWrite.
	mcpIDsByOrigin func() map[string]string

	availabilityMu      sync.Mutex
	availabilityValue   bool
	availabilityChecked time.Time
}

// NoMCPServerIDs is an mcpIDsByOrigin resolver for wirings without external
// MCP servers (tests, passthrough checkers).
func NoMCPServerIDs() map[string]string { return nil }

// New builds a Checker. papi may be nil in decision-only tests. mcpIDsByOrigin
// must be non-nil (use NoMCPServerIDs); a nil resolver is a wiring bug.
func New(client DecisionClient, papi plugin.API, mcpIDsByOrigin func() map[string]string, log Logger) *Checker {
	if mcpIDsByOrigin == nil {
		panic("accesscontrol: New requires a non-nil MCP server ID resolver (use NoMCPServerIDs)")
	}
	return &Checker{
		client:         client,
		papi:           papi,
		mcpIDsByOrigin: mcpIDsByOrigin,
		log:            log,
	}
}

// NewLegacyOnly builds the checker for servers below MinServerVersionForABAC,
// which lack the ABAC plugin APIs. Legacy access modes run their legacy checks
// unchanged; services and MCP servers are unrestricted; attribute-based agents
// fail closed (a policy may gate them and the plugin cannot find out); and
// IsAvailable reports false, hiding ABAC UI and rejecting attribute-based saves.
func NewLegacyOnly(mcpIDsByOrigin func() map[string]string, log Logger) *Checker {
	c := New(PassthroughClient{}, nil, mcpIDsByOrigin, log)
	c.legacyOnly = true
	return c
}

// errNoDecision reports a DecisionClient that returned neither a decision nor
// an error. Unreachable through either shipped client, but denying beats
// dereferencing nil inside an access gate.
var errNoDecision = errors.New("access control evaluation returned no decision")

// errInvalidSubject reports a caller that supplied a user ID no policy can be
// evaluated against.
var errInvalidSubject = errors.New("access control evaluation requires a valid user ID")

// evaluate runs one decision call.
//
// A resource ID that is not policy-addressable (a legacy UUID bot ID, or a
// caller-chosen service/MCP ID) is a designed case: no policy can be stored
// against it, so it short-circuits to no_policy and the caller applies its own
// default. A non-addressable user ID has no such case — nothing can be
// evaluated for a subject that cannot exist — so it is a caller bug and errors
// out, denying. Failing open there would hand every attribute-based resource to
// anyone who could get a malformed ID this far.
func (c *Checker) evaluate(ctx context.Context, userID, resourceType, resourceID string) (*model.AccessDecision, error) {
	if !model.IsValidId(userID) {
		logError(c.log, "ABAC evaluate called with a user ID no policy can be evaluated against", "resource_type", resourceType, "resource_id", resourceID)
		return nil, errInvalidSubject
	}
	if !model.IsValidId(resourceID) {
		logDebug(c.log, "ABAC evaluate skipped for a non-policy-addressable resource ID", "resource_type", resourceType, "resource_id", resourceID)
		decision := model.NewNoPolicyAccessDecision()
		return &decision, nil
	}

	decision, err := c.client.EvaluateAccessRequest(ctx, userID, resourceType, resourceID, ActionUse)
	if err != nil {
		return nil, err
	}
	if decision == nil {
		return nil, errNoDecision
	}
	return decision, nil
}

// CanUseAgent gates end-user use of an agent, combining the resource policy
// with the agent's UserAccessLevel. legacyCheck supplies the legacy
// allow/block outcome; attribute-based mode never invokes it. Any failure to
// obtain a decision denies unconditionally, in every agent mode.
func (c *Checker) CanUseAgent(ctx context.Context, userID string, cfg *llm.BotConfig, legacyCheck func() error) error {
	ctx, span := telemetry.Tracer().Start(ctx, "abac can_use_agent", trace.WithAttributes(
		telemetry.UserID.String(userID),
		telemetry.ABACResourceType.String(ResourceTypeAgent),
		telemetry.ABACResourceID.String(cfg.ID),
	))
	defer span.End()

	runLegacy := func() error {
		if legacyCheck != nil {
			return legacyCheck()
		}
		return nil
	}
	deny := func() error {
		err := fmt.Errorf("agent %s: %w", cfg.ID, ErrAccessDenied)
		span.RecordError(err)
		span.SetStatus(codes.Error, "access denied")
		return err
	}
	attributeBased := cfg.UserAccessLevel == llm.UserAccessLevelAttributeBased

	if c.legacyOnly {
		if attributeBased {
			// Policy existence cannot be resolved on this server: fail closed.
			logDebug(c.log, "attribute-based agent denied: server lacks ABAC support", "agent_id", cfg.ID)
			return deny()
		}
		return runLegacy()
	}

	decision, err := c.evaluate(ctx, userID, ResourceTypeAgent, cfg.ID)
	if err != nil {
		logError(c.log, "ABAC agent evaluation failed", "agent_id", cfg.ID, "error", err.Error())
		span.RecordError(err)
		return deny()
	}
	span.SetAttributes(telemetry.ABACOutcome.String(outcomeAttribute(*decision)))

	if !decision.Decision {
		return deny()
	}
	// An allow hands the verdict to the agent's own access level. Both kinds of
	// allow land here: an explicit grant, and the vacuous allow reporting that
	// no policy governs this agent — which is the deliberate fail-open for
	// attribute-based agents. The span attribute keeps the two apart.
	if attributeBased {
		return nil
	}
	return runLegacy()
}

// canUseResource is the shared service/MCP decision table: an allow (explicit,
// or the vacuous allow meaning no policy governs the resource) permits use;
// a deny or any failure to obtain a decision denies.
func (c *Checker) canUseResource(ctx context.Context, spanName, userID, resourceType, resourceID string) error {
	ctx, span := telemetry.Tracer().Start(ctx, spanName, trace.WithAttributes(
		telemetry.UserID.String(userID),
		telemetry.ABACResourceType.String(resourceType),
		telemetry.ABACResourceID.String(resourceID),
	))
	defer span.End()

	deny := func() error {
		err := fmt.Errorf("%s %s: %w", resourceType, resourceID, ErrAccessDenied)
		span.RecordError(err)
		span.SetStatus(codes.Error, "access denied")
		return err
	}

	// Legacy-only mode: services and MCP servers are unrestricted (pre-ABAC behavior).
	if c.legacyOnly {
		return nil
	}

	decision, err := c.evaluate(ctx, userID, resourceType, resourceID)
	if err != nil {
		logError(c.log, "ABAC resource evaluation failed", "resource_type", resourceType, "resource_id", resourceID, "error", err.Error())
		span.RecordError(err)
		return deny()
	}
	span.SetAttributes(telemetry.ABACOutcome.String(outcomeAttribute(*decision)))

	if !decision.Decision {
		return deny()
	}
	return nil
}

// CanUseService gates end-user use of an LLM service (by stable service ID).
func (c *Checker) CanUseService(ctx context.Context, userID, serviceID string) error {
	return c.canUseResource(ctx, "abac can_use_service", userID, ResourceTypeService, serviceID)
}

// CanUseMCPServer gates end-user visibility/use of an external MCP server
// (by stable ID).
func (c *Checker) CanUseMCPServer(ctx context.Context, userID, serverID string) error {
	return c.canUseResource(ctx, "abac can_use_mcp_server", userID, ResourceTypeMCP, serverID)
}

// availabilityProbeExpression is the expression the availability probe compiles.
// The server special-cases it to "no conditions" and returns immediately, so the
// probe costs a readiness check and one allocation.
const availabilityProbeExpression = "true"

// IsAvailable probes whether the ABAC PAP is usable, on behalf of actingUserID:
// one GetAccessControlVisualAST for a trivial expression. Only a non-nil AST
// with no error means available — that is reachable solely past the server's
// readiness gate, so open-core (no access-control service), unlicensed, and
// ABAC-disabled servers all report unavailable, as does the RPC client's silent
// (nil, nil) transport failure. The result is cached for availabilityCacheTTL
// and gates both the status endpoint (ABAC UI) and ValidateAgentWrite.
//
// Probing a policy read instead is NOT sound: the server resolves plugin policy
// ownership against a raw store read and answers an absent row with its uniform
// 404 before consulting the license or the ABAC config, and its access-control
// service is registered on any enterprise binary whatever the license. An
// unlicensed server would therefore answer 404 and look available, offering
// policy UI whose every save fails and — worse — letting attribute-based agents
// through ValidateAgentWrite to fail open for everyone.
//
// A probe needs an acting user because the server validates one on every CEL
// call. Availability itself is server-wide (license and config), so the cached
// result is shared across users; both call sites pass a request's own user.
func (c *Checker) IsAvailable(ctx context.Context, actingUserID string) bool {
	_, span := telemetry.Tracer().Start(ctx, "abac is_available")
	defer span.End()

	// Legacy-only mode has no PAP to probe; unavailable by construction.
	if c.legacyOnly {
		return false
	}

	c.availabilityMu.Lock()
	defer c.availabilityMu.Unlock()

	if !c.availabilityChecked.IsZero() && time.Since(c.availabilityChecked) < availabilityCacheTTL {
		return c.availabilityValue
	}

	available := false
	if c.papi != nil {
		ast, appErr := c.papi.GetAccessControlVisualAST(actingUserID, ResourceTypeAgent, availabilityProbeExpression)
		available = appErr == nil && ast != nil
	}
	c.availabilityValue = available
	c.availabilityChecked = time.Now()
	return available
}

// ValidateAgentWrite validates an agent create/update: it rejects
// UserAccessLevelAttributeBased when ABAC is unavailable, and rejects
// newly-assigned service/MCP references the acting user may not use. prev is
// the pre-update config (nil on create); only changed assignments are checked,
// so unrelated edits are never blocked by a since-tightened policy.
// AutoEnableNewMCPTools skips write-time MCP validation — runtime per-user
// filtering is the gate.
func (c *Checker) ValidateAgentWrite(ctx context.Context, actingUserID string, cfg, prev *llm.BotConfig) error {
	if cfg.UserAccessLevel == llm.UserAccessLevelAttributeBased && !c.IsAvailable(ctx, actingUserID) {
		return ErrABACUnavailable
	}

	if prev == nil || cfg.ServiceID != prev.ServiceID {
		if err := c.CanUseService(ctx, actingUserID, cfg.ServiceID); err != nil {
			if errors.Is(err, ErrAccessDenied) {
				return fmt.Errorf("you do not have access to the selected service: %w", err)
			}
			return err
		}
	}

	if cfg.AutoEnableNewMCPTools {
		return nil
	}

	newOrigins := newlyReferencedMCPOrigins(cfg, prev)
	if len(newOrigins) == 0 {
		return nil
	}

	rawIDsByOrigin := c.mcpIDsByOrigin()
	// Key by normalized origin: EnabledMCPTools may carry formatting variants
	// (trailing slash) of the configured BaseURL.
	idsByOrigin := make(map[string]string, len(rawIDsByOrigin))
	for origin, id := range rawIDsByOrigin {
		idsByOrigin[llm.NormalizeMCPServerOrigin(origin)] = id
	}

	for _, origin := range newOrigins {
		serverID, ok := idsByOrigin[origin]
		if !ok {
			// Embedded/plugin/unknown origins have no stable ID and are not policy-addressable.
			continue
		}
		if err := c.CanUseMCPServer(ctx, actingUserID, serverID); err != nil {
			if errors.Is(err, ErrAccessDenied) {
				return fmt.Errorf("you do not have access to the MCP server %q: %w", origin, err)
			}
			return err
		}
	}
	return nil
}

// newlyReferencedMCPOrigins returns the distinct enabled-MCP-tool origins in
// cfg not already referenced by prev (all of them on create), in first-appearance order.
func newlyReferencedMCPOrigins(cfg, prev *llm.BotConfig) []string {
	prevOrigins := make(map[string]struct{})
	if prev != nil {
		for _, t := range prev.EnabledMCPTools {
			prevOrigins[llm.NormalizeMCPServerOrigin(t.ServerOrigin)] = struct{}{}
		}
	}

	seen := make(map[string]struct{})
	var out []string
	for _, t := range cfg.EnabledMCPTools {
		origin := llm.NormalizeMCPServerOrigin(t.ServerOrigin)
		if _, ok := prevOrigins[origin]; ok {
			continue
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		out = append(out, origin)
	}
	return out
}
