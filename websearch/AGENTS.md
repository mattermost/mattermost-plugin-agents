# websearch/AGENTS.md

Scoped instructions for web search providers. Root rules in `/AGENTS.md` still apply.

## Boundary

- This package implements provider adapters such as Google and Brave.
- Runtime tool exposure and annotation decoration live in `mmtools/` and conversation flows.
- Web search config types live in `config/`.

## Commands

- Provider tests: `go test -v ./websearch/...`
- Tool integration tests: `go test -v ./mmtools/... -run WebSearch`

## Gotchas

- Use the untrusted HTTP client passed from server wiring for external fetches.
- Channel permission for native web search is enforced outside this package.
- Provider spans should reuse telemetry conventions and avoid leaking secrets.
