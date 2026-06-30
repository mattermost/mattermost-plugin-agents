---
description: Embedded Go-template system prompts and their generated name constants.
tags: [prompts, templates, go-generate, embed]
---

# prompts/AGENTS.md

Version-controlled `*.tmpl` system prompts for built-in features, embedded via `//go:embed *.tmpl` (`PromptsFolder`) and parsed by `llm.NewPrompts` at startup.

- Go `text/template`, looked up by basename without the `.tmpl` extension. Templates may include each other (e.g. `standard_personality.tmpl` → `standard_personality_without_locale.tmpl`, `locale.tmpl`).
- **After adding/removing a `.tmpl`, run `cd prompts && go generate`** — `prompts_vars.go` (the name constants) is generated; never hand-edit it.
- Built-in prompts render with `Format(name, *llm.Context)` (full context: `.BotName`, `.Tools`, …). Ad-hoc/DB prompts use `FormatString(code, data)` with `missingkey=zero`.
- Inject user-derived data through the `escapeContent` template func (`EscapePromptContent`) to neutralize `<`/`>`.
- User-created prompts (the `customprompts/` DB store) render via `FormatString` + `llm.Context.CustomPromptVars()` — a whitelist, not full context. These are different from built-ins here.

Tests: `go test ./prompts/...`.
