# bots/AGENTS.md

Scoped instructions for bot and agent runtime management. Root rules in `/AGENTS.md` still apply.

## Architecture

- `MMBots` merges file-configured bots with DB-backed agents.
- `EnsureBots` uses a cluster mutex and optimistic config caching.
- Call `ForceRefreshOnNextEnsure` after agent CRUD or cluster events that affect bot state.
- Construct bot instances through `EnsureBots`; avoid ad hoc bot creation.

## Commands

- Bot tests: `go test -v ./bots/...`

## Gotchas

- DB agent changes can require a refresh even when file config is unchanged.
- API default bot selection uses `GetBotByUsernameOrFirst`.
- License caps can affect visible/ensured agents.
