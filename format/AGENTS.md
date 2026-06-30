# format/AGENTS.md

The single place that renders Mattermost entities (posts, users, channels, teams, members, agent lists, file metadata) into text for LLM consumption or MCP tool output. Root `/AGENTS.md` applies. Never `fmt.Sprintf` `model.*` types inline in callers — add or extend a formatter here.

## Output style

Plain text with light, established markdown only (`**Post N**`, `**User N**`, `**Attached File N**`) — not XML. Timestamps are UTC `time.RFC3339`.

## Two API shapes

- **String functions** — caller passes resolved data, gets a full string: `PostBody`, `AuthoredPost`, `ThreadData`, `AgentList`, `MemberRole`.
- **Structured writers** — caller builds a `*Entry` and writes to a `*strings.Builder`: `WritePost`/`PostEntry`, `WriteUser`/`UserEntry`, `WriteChannel`/`ChannelEntry`, `WriteTeam`/`TeamEntry`, `WriteFileDescriptor`/`FileDescriptorEntry`. `BuildPostIndex` maps post IDs to 1-based indices for "(reply to Post N)" annotations.

## Adding a formatter

1. Prefer extending an existing `*Entry` + `Write*` over a new function.
2. Omit empty/zero fields using the established sentinels (e.g. `MemberCount >= 0`, `Score > 0`, `Number > 0` gate output).
3. Resolve usernames/team names at the call site and pass strings in — don't look them up inside `format/`.
4. Add table-driven tests in `format_test.go` with exact expected strings (golden-style).

## Out of scope (do not move here)

Paged file **content** (`mcpserver/tools/files.go`), small inline file bodies in `conversation/convert.go`, and eval/export text (`evals/thread_export.go`) are intentionally formatted elsewhere.
