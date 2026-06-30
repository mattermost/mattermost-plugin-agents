# channels/AGENTS.md

Scoped instructions for channel analysis flows. Root rules in `/AGENTS.md` still apply.

## Architecture

- Channel analysis creates a stored conversation, builds a completion request, runs the LLM/tool loop, and returns a stream for `streaming.Service`.
- Channel analysis needs embedded MCP tools such as `read_channel` and `get_channel_info`.
- Tool availability differs from thread analysis; do not copy thread behavior blindly.

## Commands

- Channel tests: `go test -v ./channels/...`
- Channel evals: `GOEVALS=1 go test -v ./channels -run Eval`

## Gotchas

- API routes enforce channel-analysis licensing separately.
- Balance prompt changes with eval coverage in `evals/AGENTS.md`.
