# format/AGENTS.md

Scoped instructions for LLM-facing Mattermost entity formatting. Root rules in `/AGENTS.md` still apply.

## Architecture

- This package is the single home for formatting Mattermost posts, users, channels, teams, members, and thread data for LLM prompts or tool output.
- Add new formatters here instead of formatting Mattermost model objects inline in feature packages.
- Use existing APIs such as `PostBody`, `AuthoredPost`, `ThreadData`, `PostEntry`, and `WritePost` as patterns.

## Commands

- Format tests: `go test -v ./format/...`

## Gotchas

- `PostEntry.Score > 0` intentionally enables search-result header formatting.
- `ThreadData` depends on `mmapi.ThreadData`; fetch thread metadata before formatting.
- Keep formatter output stable enough for prompts, evals, and MCP tools that consume it.
