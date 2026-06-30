# conversation/AGENTS.md

Scoped pointer for conversation entity code. Root rules in `/AGENTS.md` still apply.

- Full conversation/orchestration guidance lives in `/conversations/AGENTS.md`.
- This package owns stored conversations, turns, content blocks, request building, tool turns, attachments, and redaction defaults.
- Do not confuse this entity package with `conversations/`, which owns Mattermost event orchestration.
- Tests: `go test -v ./conversation/...`
