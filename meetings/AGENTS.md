# meetings/AGENTS.md

Scoped instructions for Calls/Zoom recording transcription and summaries. Root rules in `/AGENTS.md` still apply.

## Architecture

- The service handles recording posts and delegates conversation behavior through `conversations`.
- `ffmpeg` is required for transcription; missing ffmpeg disables transcription but should not break plugin activation.
- Initialization uses setter wiring with `conversations` to avoid cycles.

## Commands

- Meeting tests: `go test -v ./meetings/...`

## Gotchas

- Preserve legacy requester post props until streaming/webapp consumers stop depending on them.
- Do not make ffmpeg absence fatal during normal plugin startup.
