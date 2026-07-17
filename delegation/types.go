// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package delegation implements agent-to-agent task delegation: one agent
// (the delegating agent) hands a self-contained task to another agent (the
// target), which executes it as the initiating user in a visible thread in
// the initiator's DM with the target agent. The final answer is returned to
// the delegating agent as a tool result.
package delegation

import "errors"

// Request describes a single delegation. Identity fields are always sourced
// from the authenticated MCP session and server-injected call metadata —
// never from model-controlled tool arguments.
type Request struct {
	// InitiatorUserID is the human user the delegation runs on behalf of.
	// Taken from the authenticated MCP session.
	InitiatorUserID string

	// DelegatingBotUserID is the bot user ID of the agent that called
	// ask_agent. Taken from server-injected call metadata.
	DelegatingBotUserID string

	// TargetAgent is the username (with or without a leading @) or bot user
	// ID of the agent to delegate to. This is the only model-controlled
	// routing input and is re-validated server-side.
	TargetAgent string

	// Task is the self-contained task text for the target agent.
	Task string

	// ParentToolCallID is the tool_use ID of the ask_agent call on the
	// delegating agent's turn. Used to key progress events and the
	// reconciliation endpoint. Taken from server-injected call metadata.
	ParentToolCallID string
}

// Delegation progress phases, published over the delegation_update websocket
// event and returned by the status endpoint. The webapp derives an additional
// pre-approval "awaiting_approval" state client-side from the pending tool
// call status.
const (
	PhaseStarting     = "starting"
	PhaseRunning      = "running"
	PhaseWaitingOnYou = "waiting_on_you"
	PhaseCompleted    = "completed"
	PhaseFailed       = "failed"
	PhaseTimedOut     = "timed_out"
)

// Sentinel errors returned by Delegate. The wrapped error text is
// model-visible guidance, so callers should return err.Error() verbatim as
// the tool error.
var (
	// ErrNotConfigured indicates the delegation service is not (fully) wired.
	ErrNotConfigured = errors.New("delegation is not available on this server right now")

	// ErrUnknownAgent indicates the target agent could not be resolved.
	ErrUnknownAgent = errors.New("unknown agent")

	// ErrSelfDelegation indicates the delegating agent targeted itself.
	ErrSelfDelegation = errors.New("an agent cannot delegate a task to itself")

	// ErrAccessDenied indicates the initiating user may not use the target agent.
	ErrAccessDenied = errors.New("the user does not have access to this agent")

	// ErrTimedOut indicates the delegation exceeded its maximum lifetime.
	ErrTimedOut = errors.New("the delegation timed out")
)
