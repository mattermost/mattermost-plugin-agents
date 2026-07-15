// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package accesscontrol is the plugin-side PEP (policy enforcement point)
// helper for attribute-based access control over agents, LLM services, and
// external MCP servers.
//
// Layout, per the cross-repo ABAC contract (as amended by Option B):
//   - checker.go — the §9.2 decision tables (CanUseAgent/CanUseService/
//     CanUseMCPServer), write-time validation (ValidateAgentWrite), and the
//     cached PAP availability probe (IsAvailable).
//   - pdp_client.go — DecisionClient over plugin.API.EvaluateAccessControl.
//   - pap.go — policy save/get/delete and CEL editor proxying (§7).
//
// Outage invariant: `unavailable` (and any call error) always denies — the
// server resolves policy existence even when ABAC is down, so `no_policy` is
// trustworthy and legacy behavior applies exactly when no policy exists.
//
// Enforcement call sites live in bots/ (composite agent+service gate), mcp/
// (per-user server filtering), llmcontext/ (meta-tool omission), and api/
// (authoring routes, list filters) per contract §9.4.
package accesscontrol
