# format/AGENTS.md

Scoped instructions for LLM-facing Mattermost entity formatting. Root rules in `/AGENTS.md` still apply.

## Rule

- All formatting of posts, users, channels, teams, members, agents, and file descriptors for LLM consumption or MCP tool output goes through this package.
- Never `fmt.Sprintf` Mattermost model types inline in other packages; add an entry type and `Write*` helper here instead.

## API pattern

- Resolve usernames, team names, and channel names at the call site; pass plain data into entry structs.
- Prefer `WritePost`, `WriteUser`, `WriteChannel`, `WriteTeam`, `WriteFileDescriptor`, and related helpers with `strings.Builder`.
- Use `BuildPostIndex` for stable post numbering and reply annotations.
- Use convenience helpers such as `ThreadData`, `AuthoredPost`, `AgentList`, and `PostBody` when they fit.

## Parity

- Search indexing and prompt/tool display should stay aligned. `indexer` uses formatted post body content; do not invent separate post text rules elsewhere.

## Not in scope

- Timed transcripts use `subtitles.FormatForLLM`.
- Operational success/error strings from tools do not need to be centralized here.
- Prompt template bodies live in `/prompts/`.

## Commands

- Formatting tests: `go test -v ./format/...`
- Focused post formatting: `go test -v ./format/ -run TestWritePost`
