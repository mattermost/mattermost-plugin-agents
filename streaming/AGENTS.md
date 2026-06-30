# streaming/AGENTS.md

Scoped instructions for streaming LLM output into Mattermost posts. Root rules in `/AGENTS.md` still apply.

## Architecture

- `StreamToPost` handles fresh and regenerated responses.
- `StreamContinuationToPost` handles tool-approval resumes and demotes the prior anchor turn.
- The streaming layer reflects tool state; it does not execute tools.
- `turnAccumulator` persists conversation turns after stream finalization.
- `ModifyPostForBot` is the only place setting the custom LLM bot post type.
- Post props such as `conversation_id`, `responding_to`, `no_regen`, and `unsafe_links` are webapp contracts.

## Commands

- Streaming tests: `go test -v ./streaming/...`
- Benchmarks: `go test -bench=. -benchmem ./streaming/...`

## Gotchas

- `SetTurnStore` must be wired in `server/main.go` before conversation-backed streaming persists turns.
- Content blocks must be a non-nil slice so JSON encodes as `[]`.
- Use reliable cluster sends for streaming broadcasts; payloads can exceed UDP limits.
