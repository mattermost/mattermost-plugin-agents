# prompts/AGENTS.md

Built-in LLM prompt templates. Root `/AGENTS.md` applies.

## Format

- Go `text/template` files `prompts/*.tmpl`, embedded via `//go:embed *.tmpl` (`prompts.PromptsFolder`). The `prompts` package itself exposes only that embed FS plus the generated name constants — rendering lives in `*llm.Prompts`.
- Build the engine once with `llm.NewPrompts(prompts.PromptsFolder)`, then render with `engine.Format(name, *llm.Context)` where `name` is the basename without `.tmpl` (use the generated `prompts.Prompt…` constants, e.g. `prompts.PromptSearchSystem`).
- Partials: `{{template "citation_format.tmpl" .}}` — include the `.tmpl` suffix in the directive.
- The only template func is `escapeContent` (`llm.EscapePromptContent`) — apply it to any user/post/search text; never raw-interpolate untrusted content.
- Templates receive `*llm.Context`; feature-specific payload comes from `context.Parameters` (populated by the calling package before `Format`). Admin/user custom prompts are different — runtime strings rendered via `(*llm.Prompts).FormatString` with the `(*llm.Context).CustomPromptVars()` whitelist.

## Adding a template

1. Add `feature_role.tmpl` (snake_case, `_system` or `_user` suffix).
2. From `prompts/`, run `go generate` (regenerates `prompts_vars.go` constants). **There is no CI drift check** for `prompts_vars.go` — regenerate locally.
3. At the call site, render with `engine.Format(prompts.PromptFeatureRole, llmContext)` after populating `Parameters`.
