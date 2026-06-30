# threads/AGENTS.md

Scoped pointer for thread analysis. Root rules in `/AGENTS.md` still apply.

- Thread analysis follows the shared channel/thread guidance in `/channels/AGENTS.md`.
- Keep thread-specific prompts, `mmapi.GetThreadData` usage, and disabled-tool behavior aligned with that file.
- Tests: `go test -v ./threads/...`
