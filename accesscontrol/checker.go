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

// ErrAccessDenied is wrapped by every deny return.
var ErrAccessDenied = errors.New("access denied by policy")

// ErrABACUnavailable is returned when saving an agent as attribute-based while ABAC is not available.
var ErrABACUnavailable = errors.New("attribute-based access requires Attribute-Based Access Control to be licensed and enabled on this server")

const availabilityCacheTTL = 30 * time.Second

// Checker is the plugin-side PEP; PAP methods live here so api never holds a raw plugin.API.
type Checker struct {
	client DecisionClient
	papi   plugin.API // PAP calls (pap.go); nil in decision-only tests
	log    Logger

	mcpIDsByOrigin func() map[string]string // origin → stable ID for ValidateAgentWrite

	availabilityMu      sync.Mutex
	availabilityValue   bool
	availabilityChecked time.Time
}

// NoMCPServerIDs is the empty origin→ID resolver (tests, passthrough).
func NoMCPServerIDs() map[string]string { return nil }

// New builds a Checker. papi may be nil in decision-only tests. mcpIDsByOrigin
// must be non-nil (use NoMCPServerIDs).
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

// errNoDecision reports a DecisionClient that returned neither a decision nor
// an error. Unreachable through either shipped client, but denying beats
// dereferencing nil inside an access gate.
var errNoDecision = errors.New("access control evaluation returned no decision")

var errInvalidSubject = errors.New("access control evaluation requires a valid user ID")

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

// CanUseAgent combines the resource policy with cfg.UserAccessLevel.
// Attribute-based mode never invokes legacyCheck. Any failure to obtain a
// decision denies in every agent mode.
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
	// Vacuous no_policy allow is the deliberate fail-open for attribute-based
	// agents; explicit grants land here too. The span attribute keeps them apart.
	if attributeBased {
		return nil
	}
	return runLegacy()
}

// canUseResource: allow (including no_policy) permits; deny or evaluation failure denies.
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

func (c *Checker) CanUseService(ctx context.Context, userID, serviceID string) error {
	return c.canUseResource(ctx, "abac can_use_service", userID, ResourceTypeService, serviceID)
}

func (c *Checker) CanUseMCPServer(ctx context.Context, userID, serverID string) error {
	return c.canUseResource(ctx, "abac can_use_mcp_server", userID, ResourceTypeMCP, serverID)
}

// Enterprise special-cases "true" to "no conditions" without touching the CEL
// engine — cheapest expression to ask about. The probe still pays for the RPC,
// scope check, and acting-user lookup, which is what the TTL cache is for.
const availabilityProbeExpression = "true"

// IsAvailable reports whether the ABAC PAP is usable. Only a non-nil VisualAST
// means available (past the server's readiness gate). Cached for availabilityCacheTTL.
//
// A policy-read probe is unsound: the server 404s an absent row before consulting
// license or ABAC config, so an unlicensed enterprise binary would look available.
func (c *Checker) IsAvailable(ctx context.Context, actingUserID string) bool {
	_, span := telemetry.Tracer().Start(ctx, "abac is_available")
	defer span.End()

	c.availabilityMu.Lock()
	if !c.availabilityChecked.IsZero() && time.Since(c.availabilityChecked) < availabilityCacheTTL {
		cached := c.availabilityValue
		c.availabilityMu.Unlock()
		return cached
	}
	c.availabilityMu.Unlock()

	// Probe without the lock so a slow plugin RPC does not convoy other callers.
	available := false
	if c.papi != nil {
		ast, appErr := c.papi.GetAccessControlVisualAST(actingUserID, ResourceTypeAgent, availabilityProbeExpression)
		available = appErr == nil && ast != nil
	}

	c.availabilityMu.Lock()
	c.availabilityValue = available
	c.availabilityChecked = time.Now()
	c.availabilityMu.Unlock()
	return available
}

// ValidateAgentWrite rejects attribute-based mode when ABAC is unavailable, and
// newly-assigned service/MCP refs the acting user may not use. prev is nil on
// create; only changed assignments are checked. AutoEnableNewMCPTools skips
// write-time MCP checks — runtime per-user filtering is the gate.
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
			// Origins without a stable ID are not policy-addressable.
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
