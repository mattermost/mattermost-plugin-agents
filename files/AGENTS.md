# files/AGENTS.md

Scoped instructions for file content reads used by prompts and tools. Root rules in `/AGENTS.md` still apply.

## Why this package exists

- Mattermost REST file info does not expose extracted file text content.
- This package uses plugin API access and must enforce permissions explicitly.

## Security

- Always check channel permission before reading file content.
- Map forbidden access to the package's forbidden error path; do not leak file existence across channel boundaries.
- Plugin API access is admin-level, so permission checks are not optional.

## Paging

- Offsets and limits are runes, not bytes.
- Respect default and maximum read sizes.

## Prompt integration

- Large files should be represented with `format.WriteFileDescriptor`.
- Small files may be inlined through conversation conversion rules.

## Commands

- File service tests: `go test -v ./files/...`

## Pointers

- File descriptor formatting: `/format/AGENTS.md`.
- MCP `read_file` tool: `/mcpserver/AGENTS.md`.
