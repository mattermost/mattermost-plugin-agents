---
description: External LLM Bridge API client published for other Mattermost plugins.
tags: [bridge, public, api, llm, external]
---

# public/bridgeclient/AGENTS.md

Go client for the plugin's LLM Bridge API (`/bridge/v1/…`), used by other plugins (via `PluginHTTP`) and internal server code. Read `README.md` here for the consumer guide.

- **Subpath of the root module** (no separate `go.mod`); import `.../public/bridgeclient`. The Makefile clears `HAS_PUBLIC`, so `public/` is **never bundled** into the plugin tarball.
- This is an **external contract** — its streaming API exposes root `llm` types (`TextStreamResult`, `TextStreamEvent`). Breaking those breaks downstream consumers; coordinate changes and keep them in sync with the server handler `api/api_llm_bridge.go` (and its extensive tests).
- All bridge URLs use the plugin id `mattermost-ai`. Tool allowlists accept both namespaced and bare tool names (backward compat); server permission checks skip when `UserID`/`ChannelID` are empty (backward compat). Companion hook types live in `public/mcptool/`.
- `go test ./public/bridgeclient/...` (part of root `make test`).
