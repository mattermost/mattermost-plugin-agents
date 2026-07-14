// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// ErrAccessDenied is wrapped by all deny returns once WS-D wires evaluation.
var ErrAccessDenied = errors.New("access denied by policy")

// Checker is the PEP helper gating end-user access to agents, services, and
// external MCP servers. In WS-C all methods are legacy passthrough (the
// no_policy rows of the contract §9.2 decision tables); WS-D fills in the
// decision-table evaluation without changing the method signatures.
//
// When WS-D consults the PolicyIndex on unavailable/error outcomes, an index
// read error fails closed: the resource is treated as policy-gated and the
// request is denied. Only a successful Has(...) == false may fall back to
// legacy behavior.
type Checker struct {
	client DecisionClient
	index  PolicyIndex
	log    pluginapi.LogService
}

// New builds a Checker. WS-C takes the DecisionClient interface rather than
// the contract §9.1 (*pluginapi.Client, plugin.API) pair: the raw plugin.API
// handle is only needed once EvaluateAccessControl exists in server/public;
// WS-D extends the constructor when it builds the real DecisionClient.
func New(client DecisionClient, index PolicyIndex, log pluginapi.LogService) *Checker {
	return &Checker{
		client: client,
		index:  index,
		log:    log,
	}
}

// CanUseAgent gates end-user use of an agent. Combines the resource policy
// with the agent's UserAccessLevel per the contract §9.2 decision table.
// legacyCheck is the existing UsageRestrictionsForUserConfig outcome supplier,
// invoked only when the table says so.
//
// §9.2 no_policy row (current passthrough behavior): attribute-based mode
// allows (deliberate fail-open); legacy modes run the legacy check.
func (c *Checker) CanUseAgent(ctx context.Context, userID string, cfg *llm.BotConfig, legacyCheck func() error) error {
	if legacyCheck != nil {
		return legacyCheck()
	}
	return nil
}

// CanUseService gates end-user use of an LLM service (by stable service ID).
//
// §9.2 no_policy row (current passthrough behavior): allow — legacy behavior
// is unrestricted service use.
func (c *Checker) CanUseService(ctx context.Context, userID, serviceID string) error {
	return nil
}

// CanUseMCPServer gates end-user visibility/use of an external MCP server
// (by stable ID).
//
// §9.2 no_policy row (current passthrough behavior): allow — legacy behavior
// is unrestricted server visibility.
func (c *Checker) CanUseMCPServer(ctx context.Context, userID, serverID string) error {
	return nil
}

// ValidateAgentWrite validates an agent create/update: rejects
// UserAccessLevelAttributeBased when ABAC is unavailable (probe outcome ==
// unavailable via a decision call), so an agent cannot be saved into a mode
// that would hard-deny everyone.
//
// Passthrough behavior: allow — UserAccessLevelAttributeBased does not exist
// yet (contract §10 is WS-D), so there is nothing to reject.
func (c *Checker) ValidateAgentWrite(ctx context.Context, actingUserID string, cfg *llm.BotConfig) error {
	return nil
}
