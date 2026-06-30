# websearch/AGENTS.md

Scoped instructions for external web search providers. Root rules in `/AGENTS.md` still apply.

## Provider contract

- Implement `Provider.Search(ctx, query, limit)`.
- Return structured search results plus provider-specific answer text when available.
- Preserve context cancellation and tracing behavior.

## Implementations

- Brave may return a formatted answer after polling.
- Google Custom Search returns snippets and is capped by provider API limits.
- Provider errors should be useful but should not leak secrets.

## Coordination

- Built-in web search is suppressed when the selected bot has native web search enabled.
- `mmtools` exposes the LLM tool; this package owns provider calls.

## Commands

- Web search tests: `go test -v ./websearch/...`

## Pointers

- Bot native web search check: `/bots/AGENTS.md`.
- LLM tool catalog: `/llm/AGENTS.md`.
- Tool approval flow: `/conversations/AGENTS.md`.
