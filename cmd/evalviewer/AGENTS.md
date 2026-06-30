# cmd/evalviewer/AGENTS.md

Scoped instructions for the evalviewer CLI/TUI. Root rules in `/AGENTS.md` still apply.

- Canonical eval harness guidance lives in `../../evals/AGENTS.md`; read it before changing CLI behavior.
- This is a separate Go module. Build through `make evalviewer`, not `go install` from the repo root.
- Commands are `run`, `view`, `check`, and `comment`.
- Keep JSONL structs in sync with `evals/record.go`.
- `README.md` is human-facing and can lag code; prefer source and `evals/AGENTS.md` for agent work.
