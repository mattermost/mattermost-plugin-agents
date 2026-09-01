// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package accesscontrol is the plugin-side PEP for ABAC over agents, LLM
// services, and external MCP servers.
//
// Any failure to obtain a decision is an error, and every error denies.
// An allow with the no_policy reason is vacuous: legacy-mode agents fall
// through to their allow/block checks, services and MCP servers are
// unrestricted, and attribute-based agents fail open by design.
package accesscontrol
