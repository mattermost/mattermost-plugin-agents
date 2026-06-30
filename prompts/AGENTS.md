---
description: Embedded prompt templates plus generated name constants; rendered by llm.Prompts.
tags: [prompts, templates, codegen, go-embed]
---

# prompts/AGENTS.md

Embedded `text/template` prompt files and their generated name constants. The renderer lives in `llm/prompts.go`. Root `/AGENTS.md` still applies.

## Key files

- `prompts/prompts.go` — `//go:embed *.tmpl` → `PromptsFolder embed.FS`; `//go:generate` for the constants.
- `prompts/prompts_vars.go` — **generated** constants (`PromptStandardPersonality`, `PromptSearchSystem`, …). Do not hand-edit.
- `prompts/generate_prompt_vars.go` — regenerates `prompts_vars.go` from `*.tmpl` filenames.
- `prompts/*.tmpl` — template sources; compose with `{{template "other.tmpl" .}}`.

## Conventions & gotchas

- **Adding a template:**
  1. Add `my_feature_system.tmpl` under `prompts/`.
  2. `cd prompts && go generate` (or `go run generate_prompt_vars.go`) to update `prompts_vars.go`.
  3. Render via `promptsManager.Format(prompts.PromptMyFeatureSystem, llmCtx)` — the lookup key is the basename without `.tmpl`.
- Templates receive `*llm.Context` (`.RequestingUser`, `.Tools`, `.CustomInstructions`, …). Escape user-generated text with `{{escapeContent .Field}}` or pre-call `llm.EscapePromptContent`.
- Wiring: `llm.NewPrompts(prompts.PromptsFolder)` in `server/main.go`.

Tests render via the real `prompts.PromptsFolder`; renderer unit tests in `llm/prompts_test.go` use `fstest.MapFS`.
