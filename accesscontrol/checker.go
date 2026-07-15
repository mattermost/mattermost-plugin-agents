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

// availabilityCacheTTL caps how often the availability probe hits the PAP;
// probe traffic from the status route and agent saves is bursty.
const availabilityCacheTTL = 30 * time.Second

// Checker is the PEP helper gating end-user access to agents, services, and
// external MCP servers per the contract §9.2 decision tables (as amended by
// Option B). It also hosts the PAP proxying (pap.go).
type Checker struct {
	client DecisionClient
	papi   plugin.API // PAP calls (pap.go); nil in decision-only tests
	log    Logger

	// mcpIDsByOrigin resolves external MCP server origins to stable IDs for
	// ValidateAgentWrite. Injected at construction (config-backed in
	// production) so MCP assignment validation can never silently skip.
	mcpIDsByOrigin func() map[string]string

	availabilityMu      sync.Mutex
	availabilityValue   bool
	availabilityChecked time.Time
}

// NoMCPServerIDs is an mcpIDsByOrigin resolver for wirings without external
// MCP servers (tests, passthrough checkers).
func NoMCPServerIDs() map[string]string { return nil }

// New builds a Checker. papi may be nil when only decision-table evaluation
// is exercised (tests); production wiring always passes the raw plugin API.
// mcpIDsByOrigin must be non-nil (use NoMCPServerIDs when there are no
// external MCP servers); a nil resolver is a wiring bug and fails fast.
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

// evaluate runs one decision call. Invalid resource/user IDs short-circuit to
// no_policy: config bots keep UUID IDs and the core API 400s on non-26-char
// IDs; such resources can never have policies, so legacy behavior applies.
func (c *Checker) evaluate(ctx context.Context, userID, resourceType, resourceID string) (model.AccessDecisionOutcome, error) {
	if !model.IsValidId(resourceID) || !model.IsValidId(userID) {
		logDebug(c.log, "ABAC evaluate skipped for non-policy-addressable IDs", "resource_type", resourceType, "resource_id", resourceID)
		return model.AccessDecisionOutcomeNoPolicy, nil
	}
	return c.client.EvaluateAccessRequest(ctx, userID, resourceType, resourceID, ActionUse)
}

// CanUseAgent gates end-user use of an agent. Combines the resource policy
// with the agent's UserAccessLevel per the contract §9.2 decision table.
// legacyCheck is the existing UsageRestrictionsForUserConfig outcome supplier,
// invoked only when the table says so; attribute-based mode never invokes it
// (UserIDs/TeamIDs are ignored in that mode).
//
// The server resolves policy existence even when ABAC is unavailable, so
// no_policy is trustworthy and unavailable means a policy exists (or
// existence could not be determined): unavailable — like any call error —
// denies unconditionally, in every agent mode.
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

// canUseResource implements the mode-less §9.2 table shared by services and
// MCP servers: allow/no_policy → nil; deny/unavailable/error/unknown → deny.
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
// for a freshly generated (guaranteed-nonexistent) ID. When ABAC is up the
// store miss collapses to a 404 (available); open core / unlicensed / disabled
// servers return a non-404 error (unavailable). A decision-call probe cannot
// distinguish these anymore: the server resolves policy existence even while
// ABAC is down, so a nonexistent ID yields no_policy either way. The result is
// cached for availabilityCacheTTL.
//
// This aligns the probe with what it gates: the status endpoint shows/hides
// authoring UI and ValidateAgentWrite rejects saving into a mode the PAP/PDP
// cannot serve — both exactly PAP availability.
func (c *Checker) IsAvailable(ctx context.Context) bool {
	_, span := telemetry.Tracer().Start(ctx, "abac is_available")
	defer span.End()

	c.availabilityMu.Lock()
	defer c.availabilityMu.Unlock()

	if !c.availabilityChecked.IsZero() && time.Since(c.availabilityChecked) < availabilityCacheTTL {
		return c.availabilityValue
	}

	available := false
	if c.papi != nil {
		_, appErr := c.papi.GetAccessControlPolicy(model.NewId())
		// A nil appErr would mean a policy exists under the fresh ID —
		// practically impossible, but it still proves the PAP answered.
		available = appErr == nil || isNotFoundAppErr(appErr)
	}
	c.availabilityValue = available
	c.availabilityChecked = time.Now()
	return available
}

// ValidateAgentWrite validates an agent create/update per contract §9.1:
//   - rejects UserAccessLevelAttributeBased when ABAC is unavailable, so an
//     agent cannot be saved into a mode §9.2 would hard-deny;
//   - rejects newly-assigned service/MCP references the acting user may not
//     use. prev is the pre-update config (nil on create): create validates
//     every assignment, update only the changed ServiceID and newly-added MCP
//     origins, so unrelated edits are never blocked by a since-tightened
//     policy on a pre-existing assignment.
//
// AutoEnableNewMCPTools skips write-time MCP validation entirely: the flag
// grants future, unknowable servers; runtime per-user filtering is the gate.
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
	// Key by normalized origin: EnabledMCPTools entries may carry formatting
	// variants (trailing slash) of the configured BaseURL.
	idsByOrigin := make(map[string]string, len(rawIDsByOrigin))
	for origin, id := range rawIDsByOrigin {
		idsByOrigin[llm.NormalizeMCPServerOrigin(origin)] = id
	}

	for _, origin := range newOrigins {
		serverID, ok := idsByOrigin[origin]
		if !ok {
			// Embedded/plugin/unknown origins have no stable ID and are not
			// policy-addressable — matches the runtime filter.
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

// newlyReferencedMCPOrigins returns the distinct ServerOrigins in cfg's
// enabled MCP tools that are not already referenced by prev (all of them on
// create). Order follows first appearance for deterministic error messages.
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
