// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package accesscontrol is the plugin-side PEP (policy enforcement point)
// helper for attribute-based access control over agents, LLM services, and
// external MCP servers.
//
// Layout, per the cross-repo ABAC contract:
//   - checker.go — the §9.2 decision tables (CanUseAgent/CanUseService/
//     CanUseMCPServer), write-time validation (ValidateAgentWrite), and the
//     cached PDP availability probe (IsAvailable).
//   - pdp_client.go — DecisionClient over plugin.API.EvaluateAccessControl.
//   - pap.go — policy save/get/delete and CEL editor proxying (§7), colocated
//     with the policy-index bookkeeping so a successful save/delete can never
//     skip the index update.
//   - kv_policy_index.go — the persisted policy index (§9.3), consulted only
//     on unavailable/error outcomes to decide whether to fail closed.
//
// Enforcement call sites live in bots/ (composite agent+service gate), mcp/
// (per-user server filtering), llmcontext/ (meta-tool omission), and api/
// (authoring routes, list filters) per contract §9.4.
package accesscontrol
