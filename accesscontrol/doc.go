// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package accesscontrol is the plugin-side policy enforcement point for
// attribute-based access control over agents, LLM services, and external MCP
// servers: decision tables and the availability probe (checker.go), decision
// calls (pdp_client.go), and policy CRUD / CEL editor proxying (pap.go).
//
// Invariants: any failure to obtain a decision — an unavailable PDP included —
// is an error, and every error denies. Unavailability is no longer a decision
// value the PDP can report. A decision follows OpenID AuthZEN: a boolean plus
// a context, where an allow carrying the no_policy reason (AccessDecision's
// IsNoPolicy) is vacuous — no policy governs the resource. That answer is
// trustworthy, because the server resolves policy existence even when ABAC is
// down: legacy-mode agents run their legacy allow/block checks, services and
// MCP servers are unrestricted, and attribute-based agents fail open by
// design. On servers below MinServerVersionForABAC (NewLegacyOnly,
// version-gated) policy existence cannot be resolved, so attribute-based
// agents deny.
//
// Enforcement call sites: bots/ (agent+service gate), mcp/ (per-user server
// filtering), llmcontext/ (meta-tool omission), api/ (authoring, list filters).
package accesscontrol
