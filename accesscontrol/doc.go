// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package accesscontrol is the plugin-side PEP (policy enforcement point)
// helper for attribute-based access control over agents, LLM services, and
// external MCP servers.
//
// This package currently ships only the scaffolding defined by the cross-repo
// ABAC contract (§9): the Checker helpers behave as pure legacy passthrough
// (the no_policy rows of the contract §9.2 decision tables) and no enforcement
// is wired anywhere. A follow-up workstream provides the real DecisionClient
// (plugin.API.EvaluateAccessControl), the persisted PolicyIndex, and the call
// sites (§9.4), swapping the method bodies without changing any signature.
package accesscontrol
