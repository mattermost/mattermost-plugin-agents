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

// availabilityCacheTTL caps how often the availability probe hits the PDP;
// probe traffic from the status route and agent saves is bursty.
const availabilityCacheTTL = 30 * time.Second

// Checker is the PEP helper gating end-user access to agents, services, and
// external MCP servers per the contract §9.2 decision tables. It also hosts
// the PAP proxying (pap.go) so policy saves and index bookkeeping cannot be
// separated.
type Checker struct {
	client DecisionClient
	papi   plugin.API // PAP calls (pap.go); nil in decision-only tests
	index  PolicyIndex
	log    Logger

	// mcpIDsByOrigin resolves external MCP server origins to stable IDs for
	// ValidateAgentWrite. Injected at construction (config-backed in
	// production) so MCP assignment validation can never silently skip.
	mcpIDsByOrigin func() map[string]string

	// mutationMu serializes whole PAP mutations (policy save/delete plus the
	// index bookkeeping) so no interleaving can produce an enforced policy
	// without its fail-closed index marker. Production passes the
	// PolicyIndexMutexKey cluster mutex; tests may pass a plain sync.Mutex.
	mutationMu sync.Locker

	// reconciler runs decision-time index self-healing in the background so
	// the authorization hot path never blocks on the KV-backed index for
	// definitive outcomes. Stopped by Close.
	reconciler *reconciler

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
// mutationMutex must be the PolicyIndexMutexKey cluster mutex in production;
// nil falls back to a process-local mutex (single-instance tests only).
func New(client DecisionClient, papi plugin.API, index PolicyIndex, mcpIDsByOrigin func() map[string]string, mutationMutex sync.Locker, log Logger) *Checker {
	if mcpIDsByOrigin == nil {
		panic("accesscontrol: New requires a non-nil MCP server ID resolver (use NoMCPServerIDs)")
	}
	if mutationMutex == nil {
		mutationMutex = &sync.Mutex{}
	}
	c := &Checker{
		client:         client,
		papi:           papi,
		index:          index,
		mcpIDsByOrigin: mcpIDsByOrigin,
		mutationMu:     mutationMutex,
		reconciler:     newReconciler(defaultReconcileCooldown),
		log:            log,
	}
	go c.reconciler.run(c.reconcileIndex)
	return c
}

// Close stops the background reconciliation worker, waiting for an in-flight
// reconciliation to finish. Call on plugin deactivation. Idempotent.
func (c *Checker) Close() {
	c.reconciler.close()
}

// evaluate runs one decision call. Invalid resource/user IDs short-circuit to
// no_policy: config bots keep UUID IDs and the core API 400s on non-26-char
// IDs; such resources can never have policies, so legacy behavior applies.
func (c *Checker) evaluate(ctx context.Context, userID, resourceType, resourceID string) (Outcome, error) {
	if !model.IsValidId(resourceID) || !model.IsValidId(userID) {
		logDebug(c.log, "ABAC evaluate skipped for non-policy-addressable IDs", "resource_type", resourceType, "resource_id", resourceID)
		return OutcomeNoPolicy, nil
	}
	return c.client.EvaluateAccessRequest(ctx, userID, resourceType, resourceID, ActionUse)
}

// indexGated reports whether the resource must fail closed on an
// unavailable/error outcome: true when the policy index has a marker for it
// OR the index itself cannot be read (unknowable = gated).
func (c *Checker) indexGated(resourceType, resourceID string) bool {
	has, err := c.index.Has(resourceType, resourceID)
	if err != nil {
		return true
	}
	return has
}

// scheduleReconcile hands a definitive decision outcome to the background
// reconciliation worker. The hot path must never block on the KV-backed
// index for allow/deny/no_policy outcomes: enqueue is in-memory bookkeeping
// plus a non-blocking buffered channel send; the actual index reads/writes
// happen in reconcileIndex on the worker goroutine.
func (c *Checker) scheduleReconcile(resourceType, resourceID string, outcome Outcome) {
	if outcome == OutcomeUnavailable {
		// No server truth to reconcile against.
		return
	}
	if !model.IsValidId(resourceID) {
		// Short-circuited evaluations (config bots, invalid IDs) are never
		// policy-addressable and can never carry a marker.
		return
	}
	c.reconciler.enqueue(resourceType, resourceID, outcome)
}

// reconcileIndex self-heals the fail-closed policy index from server truth
// after a decision. The cluster mutex serializing PAP mutations uses a
// renewable lease; if the lease is lost mid-mutation, an interleaving with a
// concurrent opposing mutation can leave the index divergent from the stored
// policies (e.g. an enforced policy without its marker). Server-side fencing
// is not available through the plugin API, so instead of trying to win that
// race we eliminate its PERSISTENCE: every decision with a definitive outcome
// schedules a reconciliation of the marker for the resource it evaluated,
// processed here on the background worker (see reconciler).
//
//   - allow/deny means the PDP evaluated a stored policy — the marker must
//     exist. Adding is the fail-closed direction and needs no confirmation.
//   - no_policy suggests the marker is stale, but the outcome may predate an
//     in-flight save, so removal is confirmed against server truth (a Get
//     returning 404) under the mutation mutex before the marker is dropped.
//
// Writes happen only when the index diverges. The residual hazard window —
// lease loss AND a concurrent opposing mutation AND an ABAC outage before the
// next successful decision or plugin activation — is an accepted,
// self-correcting risk (see also RebuildIndex and DeletePolicy).
func (c *Checker) reconcileIndex(resourceType, resourceID string, outcome Outcome) {
	if !model.IsValidId(resourceID) {
		// Short-circuited evaluations (config bots, invalid IDs) are never
		// policy-addressable and can never carry a marker.
		return
	}

	switch outcome {
	case OutcomeAllow, OutcomeDeny:
		if has, err := c.index.Has(resourceType, resourceID); err != nil || has {
			return
		}
		c.mutationMu.Lock()
		defer c.mutationMu.Unlock()
		if has, err := c.index.Has(resourceType, resourceID); err != nil || has {
			return
		}
		if err := c.index.Add(resourceType, resourceID); err != nil {
			logWarn(c.log, "ABAC policy index self-heal failed to restore a missing marker",
				"resource_type", resourceType, "resource_id", resourceID, "error", err.Error())
			return
		}
		logWarn(c.log, "ABAC policy index self-healed: restored a missing fail-closed marker for an enforced policy",
			"resource_type", resourceType, "resource_id", resourceID)
	case OutcomeNoPolicy:
		if has, err := c.index.Has(resourceType, resourceID); err != nil || !has {
			return
		}
		if c.papi == nil {
			return
		}
		c.mutationMu.Lock()
		defer c.mutationMu.Unlock()
		if has, err := c.index.Has(resourceType, resourceID); err != nil || !has {
			return
		}
		// Confirm against server truth under the mutex: the no_policy outcome
		// may predate a save that committed in the meantime.
		if _, appErr := c.papi.GetAccessControlPolicy(resourceID); !isNotFoundAppErr(appErr) {
			return
		}
		if err := c.index.Remove(resourceType, resourceID); err != nil {
			logWarn(c.log, "ABAC policy index self-heal failed to drop a stale marker",
				"resource_type", resourceType, "resource_id", resourceID, "error", err.Error())
			return
		}
		logWarn(c.log, "ABAC policy index self-healed: dropped a stale fail-closed marker with no stored policy",
			"resource_type", resourceType, "resource_id", resourceID)
	case OutcomeUnavailable:
		// No server truth to reconcile against.
	}
}

// CanUseAgent gates end-user use of an agent. Combines the resource policy
// with the agent's UserAccessLevel per the contract §9.2 decision table.
// legacyCheck is the existing UsageRestrictionsForUserConfig outcome supplier,
// invoked only when the table says so; attribute-based mode never invokes it
// (UserIDs/TeamIDs are ignored in that mode).
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
		if attributeBased || c.indexGated(ResourceTypeAgent, cfg.ID) {
			return deny()
		}
		return runLegacy()
	}
	span.SetAttributes(telemetry.ABACOutcome.String(string(outcome)))
	c.scheduleReconcile(ResourceTypeAgent, cfg.ID, outcome)

	switch outcome {
	case OutcomeAllow:
		if attributeBased {
			return nil
		}
		return runLegacy()
	case OutcomeDeny:
		return deny()
	case OutcomeNoPolicy:
		// Deliberate fail-open for attribute-based agents without a policy.
		if attributeBased {
			return nil
		}
		return runLegacy()
	case OutcomeUnavailable:
		if attributeBased || c.indexGated(ResourceTypeAgent, cfg.ID) {
			return deny()
		}
		return runLegacy()
	default:
		// Unknown outcome from a future server: fail toward the closed row.
		if attributeBased || c.indexGated(ResourceTypeAgent, cfg.ID) {
			return deny()
		}
		return runLegacy()
	}
}

// canUseResource implements the mode-less §9.2 table shared by services and
// MCP servers: allow/no_policy → nil; deny → ErrAccessDenied;
// unavailable/error → deny only when the policy index gates the resource.
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
		if c.indexGated(resourceType, resourceID) {
			return deny()
		}
		return nil
	}
	span.SetAttributes(telemetry.ABACOutcome.String(string(outcome)))
	c.scheduleReconcile(resourceType, resourceID, outcome)

	switch outcome {
	case OutcomeAllow, OutcomeNoPolicy:
		return nil
	case OutcomeDeny:
		return deny()
	case OutcomeUnavailable:
		if c.indexGated(resourceType, resourceID) {
			return deny()
		}
		return nil
	default:
		if c.indexGated(resourceType, resourceID) {
			return deny()
		}
		return nil
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

// IsAvailable probes whether the ABAC PDP is usable: one decision call for a
// freshly generated (guaranteed-nonexistent) agent ID, expecting no_policy
// when ABAC is up and unavailable (or an error) when it is not. The result is
// cached for availabilityCacheTTL.
func (c *Checker) IsAvailable(ctx context.Context, userID string) bool {
	c.availabilityMu.Lock()
	defer c.availabilityMu.Unlock()

	if !c.availabilityChecked.IsZero() && time.Since(c.availabilityChecked) < availabilityCacheTTL {
		return c.availabilityValue
	}

	outcome, err := c.evaluate(ctx, userID, ResourceTypeAgent, model.NewId())
	available := err == nil && outcome != OutcomeUnavailable
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
