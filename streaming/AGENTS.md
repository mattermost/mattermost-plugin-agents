# streaming/AGENTS.md

Scoped pointer for Mattermost post streaming. Root rules in `/AGENTS.md` still apply.

- Full generation and streaming guidance lives in `/conversations/AGENTS.md`.
- Preserve reliable websocket broadcasts, conversation post props, and assistant-turn persistence behavior.
- Benchmarks: `go test -bench=. -benchmem ./streaming/...`
- Tests: `go test -v ./streaming/...`
