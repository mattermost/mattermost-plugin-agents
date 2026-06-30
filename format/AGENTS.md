---
description: Single home for formatting Mattermost entities (posts, users, channels, teams, files, threads) for LLM/tool consumption.
tags: [format, posts, users, channels, llm-formatting]
---

# format/AGENTS.md

The only place Mattermost model types are rendered to text for LLMs and tool output. Root `/AGENTS.md` still applies.

## Pattern

- **Simple helpers:** `PostBody`, `AuthoredPost`, `ThreadData`, `AgentList`, `MemberRole`, `BuildPostIndex`.
- **Structured writers:** an entry struct (`PostEntry`, `UserEntry`, `ChannelEntry`, `TeamEntry`, `FileDescriptorEntry`) plus a `Write*` function taking a `*strings.Builder` (`WritePost`, `WriteUser`, `WriteChannel`, `WriteTeam`, `WriteFileDescriptor`).

## Conventions & gotchas

- **Never `fmt.Sprintf` a `model.*` type inline** in feature packages. Add or reuse a formatter here. Callers across `threads/`, `channels/`, `conversation/`, `mcpserver/tools/`, `indexer/` rely on this.
- `mmapi.ThreadData` holds **raw** posts; `format.ThreadData` is the LLM-facing rendering — don't confuse the two.
- `PostBody` is the canonical post-text extraction (includes attachment text/fields).
- To support a new entity: add an entry type + a `Write*` function, then call it from the feature package.
