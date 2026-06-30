---
description: Builds *llm.Context from Mattermost state, including MCP tool filtering and interactive gating.
tags: [llm, context, mcp, tools]
---

# llmcontext/AGENTS.md

Assembles `*llm.Context` from Mattermost state (server/user/channel/bot + tools/MCP) via `Builder` and `llm.ContextOption` functions. Never mutate a partial `llm.Context` by hand for tools — go through the builder.

- **MCP tool filtering order:** admin policy → agent allowlist (`AutoEnableNewMCPTools`) → disabled origins → `KeepMCPTool` predicate → registry/insert. User profile fields are sanitized before prompt render.
- **`MCPDynamicToolLoading`** (default true on legacy bots) switches to meta-tools (`search_tools` / `load_tool`) backed by a BM25 registry instead of inlining every tool schema. `WithLLMContextConcreteTools` forces full MCP schemas (bridge catalog APIs).
- **`WithLLMContextInteractive()` is only for human-interactive flows** (enables tools like ask-user-question); automated paths must not set it.
- **`WithLLMContextNoTools`** overrides a bot's `DisableTools` for inter-plugin/no-tool calls.
- Fetching MCP tools requires a non-nil Go `context.Context`; a nil one logs an error and skips MCP.
