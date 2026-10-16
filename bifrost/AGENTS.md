# bifrost/AGENTS.md

Scoped instructions for building LLM requests in `bifrost/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Bifrost replays the same request against each service in the fallback chain, so any provider- or model-dependent shaping (reasoning settings, native tools, image limits, storage flags, API mode, structured output) must be valid for every service in the chain, not just the primary.
- Every request satisfies the strictest provider's message rules, for replayed history as well as the new turn: each tool call has a result paired by ID (not position), no text block is empty, and each declared image MIME type matches the bytes.
