// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package accesscontrol is the plugin-side PEP for ABAC over agents, LLM
// services, and external MCP servers.
//
// Any failure to obtain a decision is an error, and every error denies.
// An allow with the no_policy reason is vacuous: legacy-mode agents fall
// through to their allow/block checks, and services and MCP servers are
// unrestricted. Attribute-based agents deny: the policy is the sole user-
// access gate, so a missing policy is not a grant.
//
// Enforcement call sites: bots/ (agent+service gate), mcp/ (per-user server
// filtering), api/ (authoring, list filters), mcpserver/ (external HTTP tool
// filtering).
package accesscontrol
