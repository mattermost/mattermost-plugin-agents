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

// evaluate runs one decision call. Non-26-char IDs (e.g. legacy UUID bot IDs)
// can never have policies, so they short-circuit to no_policy.
func (c *Checker) evaluate(ctx context.Context, userID, resourceType, resourceID string) (model.AccessDecisionOutcome, error) {
	if !model.IsValidId(resourceID) || !model.IsValidId(userID) {
		logDebug(c.log, "ABAC evaluate skipped for non-policy-addressable IDs", "resource_type", resourceType, "resource_id", resourceID)
		return model.AccessDecisionOutcomeNoPolicy, nil
	}
	return c.client.EvaluateAccessRequest(ctx, userID, resourceType, resourceID, ActionUse)
}

// CanUseAgent gates end-user use of an agent, combining the resource policy
// with the agent's UserAccessLevel. legacyCheck supplies the legacy
// allow/block outcome; attribute-based mode never invokes it. Unavailable —
// like any call error — denies unconditionally, in every agent mode.
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

	outcome, err := c.evaluate(ctx, userID, ResourceTypeAgent, cfg.ID)
	if err != nil {
		logError(c.log, "ABAC agent evaluation failed", "agent_id", cfg.ID, "error", err.Error())
		span.RecordError(err)
		return deny()
	}
	span.SetAttributes(telemetry.ABACOutcome.String(string(outcome)))

	switch outcome {
	case model.AccessDecisionOutcomeAllow:
		if attributeBased {
			return nil
		}
		return runLegacy()
	case model.AccessDecisionOutcomeDeny:
		return deny()
	case model.AccessDecisionOutcomeNoPolicy:
		// Deliberate fail-open for attribute-based agents without a policy.
		if attributeBased {
			return nil
		}
		return runLegacy()
	case model.AccessDecisionOutcomeUnavailable:
		return deny()
	default:
		// Unknown outcome from a future server: fail closed.
		return deny()
	}
}

// canUseResource is the shared service/MCP decision table:
// allow/no_policy → nil; deny/unavailable/error/unknown → deny.
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

	outcome, err := c.evaluate(ctx, userID, resourceType, resourceID)
	if err != nil {
		logError(c.log, "ABAC resource evaluation failed", "resource_type", resourceType, "resource_id", resourceID, "error", err.Error())
		span.RecordError(err)
		return deny()
	}
	span.SetAttributes(telemetry.ABACOutcome.String(string(outcome)))

	switch outcome {
	case model.AccessDecisionOutcomeAllow, model.AccessDecisionOutcomeNoPolicy:
		return nil
	case model.AccessDecisionOutcomeDeny, model.AccessDecisionOutcomeUnavailable:
		return deny()
	default:
		return deny()
	}
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

// IsAvailable probes whether the ABAC PAP is usable: one GetAccessControlPolicy
// for a fresh (nonexistent) ID. A 404 means the PAP answered (available); open
// core / unlicensed / disabled servers return a non-404 error (unavailable).
// The result is cached for availabilityCacheTTL and gates both the status
// endpoint (ABAC UI) and ValidateAgentWrite.
func (c *Checker) IsAvailable(ctx context.Context) bool {
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
		policy, appErr := c.papi.GetAccessControlPolicy(model.NewId())
		// 404 (or an improbable hit) proves the PAP answered; (nil, nil) is
		// the RPC client's silent transport failure and must map to unavailable.
		available = isNotFoundAppErr(appErr) || (appErr == nil && policy != nil)
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
	if cfg.UserAccessLevel == llm.UserAccessLevelAttributeBased && !c.IsAvailable(ctx) {
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
