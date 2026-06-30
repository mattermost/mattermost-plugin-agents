---
description: Agent/bot registry, EnsureBots lifecycle, and LLM service resolution.
tags: [bots, agents, license, llm]
---

# bots/AGENTS.md

Ensures Mattermost bot users and in-memory `Bot` wrappers, enforces access restrictions, and resolves LLM services (building the Bifrost-backed wrapper chain in `getBaseLLM`).

- **Two agent sources are merged:** `config.GetBots()` (capped to 1 without a multi-LLM license) and `agentStore.ListAgents()` (DB-backed user agents, not license-capped, API-gated).
- **Use `Bot.GetService()`** for the resolved service — the embedded `cfg.Service` is internal. Fallback-chain services are included in change detection.
- `EnsureBots` has an optimistic fast-path that skips the `ai_ensure_bots` cluster mutex when configs are unchanged; agent CRUD/cluster events set `ForceRefreshOnNextEnsure` to invalidate it. Access checks: `CheckUsageRestrictions` / `UsageRestrictionsForUserConfig` (`permissions.go`).
- `go test ./bots/...`.
