# llm/AGENTS.md

Scoped instructions for the model, prompt, provider, and context stack. Root rules in `/AGENTS.md` still apply.

## Scope

- Applies to `llm/`, `bifrost/`, `prompts/`, `llmcontext/`, and `customprompts/`.
- Generation orchestration and Mattermost post streaming live in `/conversations/AGENTS.md`.

## Architecture

- `llm.LanguageModel` is the provider contract. Real providers go through `bifrost.LLM`; load tests use `loadtest.MockLLM`.
- Bot wrapper order is set in `bots.getLLM`; wrappers that intercept `ChatCompletionNoStream` must sit outside token usage logging.
- OpenAI-compatible provider entries belong in `llm/providers.go`; do not special-case each compatible provider throughout the codebase.
- Provider errors exposed to logs or clients must pass through `llm.SanitizeProviderError` or `SanitizeProviderErrorMessage`.
- Spans in provider paths use root tracing rules and attribute keys from `telemetry/attributes.go`.

## Prompts

- Templates live in `prompts/*.tmpl` and are embedded through `prompts.PromptsFolder`.
- Add a template, then run `go generate` in `prompts/` to update prompt constants.
- Template composition should use `{{template "name" .}}`.
- User content in templates must use the `escapeContent` func.
- Custom prompts render through `prompts.FormatString(template, ctx.CustomPromptVars())`; expose only whitelisted context variables.

## Context and tools

- `llmcontext.Builder` assembles `llm.Context` through options; set catalog flags before adding tools.
- `WithLLMContextInteractive()` is for human-present flows only.
- Bridge/catalog APIs that need full MCP schemas should use concrete tools with force-concrete behavior.
- Tool definitions live in `llm/tools.go`; use bound params and call metadata helpers instead of ad hoc maps.
- Truncation must never drop system posts.

## Commands

- LLM contracts: `go test -v ./llm/...`
- Provider gateway: `go test -v ./bifrost/...`
- Context builder: `go test -v ./llmcontext/...`
- Prompt templates: `go test -v ./prompts/...`
- Custom prompts: `go test -v ./customprompts/...`
- Benchmarks: `go test -bench=. -benchmem ./llm/...`

## Pointers

- Conversation orchestration and streaming: `/conversations/AGENTS.md`.
- Bot wrapper wiring: `/bots/AGENTS.md`.
- Prompt evals: `/evals/AGENTS.md`.
