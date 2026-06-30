---
description: Bot/agent management — merges config bots with DB user agents, ensures Mattermost bot users, resolves services to LLMs.
tags: [bots, agents, license, ensure-bots]
---

# bots/AGENTS.md

Merges config-defined bots with DB user agents, ensures Mattermost bot users exist, and resolves services to `llm.LanguageModel` instances. Root `/AGENTS.md` still applies.

## Key files

- `bots/bots.go` — `MMBots`, `New`, `EnsureBots`, `snapshotBotsAndServices`, `getLLM` (applies the wrapper chain).
- `bots/bot.go` — the `Bot` wrapper.
- `bots/permissions.go`, `bots/mentions.go` — access levels; mention detection.

## Conventions & gotchas

- **Use `Bot.GetService()`**, not `cfg.ServiceID` directly, to resolve a bot's service.
- **License:** config bots are capped at 1 without a multi-LLM license; **DB user agents bypass that cap**.
- **`EnsureBots`** runs under a cluster mutex with an optimistic equality skip; cluster agent events set `ForceRefreshOnNextEnsure`.
- **Wrapper chain:** `getLLM` wraps the base LLM (truncation → token logging → structured-output fallback). Order matters — see `llm/AGENTS.md`.
- Agent **permissions** (`PermissionManageOwnAgent`, etc.) are enforced in `api/api_agents.go`, not here.
