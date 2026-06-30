---
description: The mandatory formatters for Mattermost entities consumed by LLMs, MCP tools, and the indexer.
tags: [format, llm, mcp, posts]
---

# format/AGENTS.md

The single home (`format.go`) for turning Mattermost entities (posts, users, channels, teams, members, files) into text for LLM consumption or tool output. **Add a formatter here; never `fmt.Sprintf` model types inline** (root rule).

- Structured output uses `Write*(w, *Entry)` helpers with optional fields (e.g. `WritePost`, `WriteUser`, `WriteChannel`, `WriteTeam`, `WriteFileDescriptor`). Timestamps are RFC3339 UTC derived from millisecond `CreateAt`.
- **`PostBody(post)` is also the indexer's canonical text** — changing it alters embedding content and requires a reindex.
- Small but high-leverage: new MCP tools and conversation paths must route entity output through here. Tests: `go test ./format/...`.
