// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package audit

// Events emitted outside the gin audit middleware. They are declared here so
// every package that emits them, and the API registry, share one name.
const (
	// EventAgentScopedSessionGrant: an agent's bot was granted a scoped
	// session for one channel-triggered run. The actor is the requester.
	EventAgentScopedSessionGrant = "agentScopedSessionGrant"

	// EventAgentScopedToolCall: a scoped run auto-ran a tool without asking
	// the requester. The actor is the requester.
	EventAgentScopedToolCall = "agentScopedToolCall"
)
