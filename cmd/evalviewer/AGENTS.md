---
description: Cobra + Bubbletea TUI/runner for prompt evals (nested Go module).
tags: [evals, cli, tui, go-module]
---

# cmd/evalviewer/AGENTS.md

CLI runner and TUI for the prompt-eval harness. See `README.md` here for full usage and `evals/AGENTS.md` for harness conventions.

- This is a **nested Go module** (`cmd/evalviewer/go.mod`), built via `make evalviewer`. It is not covered by the root `go test ./...`; `make check-go-mods` does not tidy it either, so run `go mod tidy` here manually if you change its deps.
- Subcommands: `run` (TUI, injects `GOEVALS=1`), `check` (non-interactive, exit 1 on failure), `comment` (writes `comment.md`, exit 0), `view` (display existing `evals.jsonl`).
- `run`/`check`/`comment` delete `evals.jsonl` first, then re-run the eval packages; the viewer searches cwd and parents for the file.
