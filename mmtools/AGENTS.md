---
description: Built-in (non-MCP) plugin tools exposed alongside MCP tools.
tags: [tools, websearch, llm]
---

# mmtools/AGENTS.md

Plugin-native tools (not MCP protocol) surfaced to the LLM via `MMToolProvider`. Wired into `llmcontext.NewLLMContextBuilder` in `server/main.go`.

- `GetTools(bot, llmContext)` is a catalog-vs-execution split. Web-search tools are omitted when the bot has native provider web search (`hasNativeWebSearch`).
- `AskUserQuestion` is only offered when `llmContext.ToolCatalog.InteractiveUserPresent`; its resolver is an error backstop — real answers come through the approval flow (`llm.UserInteractionSelect`).
- Add a tool by implementing it here, returning it from `GetTools`, defining its schema with `llm.NewJSONSchemaFromStruct`, and routing any user-facing strings through i18n.
- Web-search providers themselves live in `websearch/` (Google/Brave, each with its own OTel span).
