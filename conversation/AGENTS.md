# conversation/AGENTS.md

Scoped instructions for the persisted conversation entity/service layer. Root rules in `/AGENTS.md` apply.

## Scope

- `conversation/` is singular: conversation records, turns, `CompletionRequest` building, title generation, and file-inline limits.
- `conversations/` is plural: Mattermost hook orchestration, approvals, streaming, and regeneration.

## Commands

- Unit tests: `go test -v ./conversation/...`.
- Related orchestration tests: `go test -v ./conversations/...`.

## Conventions

- Keep persistence-facing types separate from Mattermost orchestration behavior.
- Preserve context threading on request-scoped paths; async title generation is fire-and-forget.
- Formatting Mattermost entities for LLM prompts goes through `format/`.

## Pointers

- Orchestration layer: `/conversations/AGENTS.md`.
- LLM request/operation conventions: `/llm/AGENTS.md`.
