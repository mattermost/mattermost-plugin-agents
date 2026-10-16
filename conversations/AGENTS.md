# conversations/AGENTS.md

Scoped instructions for the conversation turn lifecycle (`conversations/`, `toolrunner/`, `streaming/`). Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Re-check the requesting user's current access to permission-gated source content (channels, files, meeting recordings and transcripts) on every path that replays it — regenerate, follow-up turns, conversation fetch — not only when it was first read.
- Stop and regenerate end the whole run, including tool calls it already started; end-of-run cleanup affects only its own run, never a newer run on the same post.
