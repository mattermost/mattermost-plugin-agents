# mcp/AGENTS.md

Scoped instructions for the MCP client in `mcp/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Everything obtained through a user's own MCP session (server info, instructions, tool lists and descriptions, tool results) is scoped to that user: never write it to shared caches or KV keys, or anywhere another user's request can read it.
- Attach OAuth credentials only to requests for the server's pinned origin, never on cross-origin redirects; disconnect revokes the grant at the provider when it advertises revocation.
