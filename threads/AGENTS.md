# threads/AGENTS.md

Scoped instructions for thread analysis flows. Root rules in `/AGENTS.md` still apply.

## Architecture

- Thread analysis fetches thread metadata through `mmapi`, formats it through `format.ThreadData`, creates a stored conversation, and returns a stream for `streaming.Service`.
- Tools are disabled for thread analysis unless explicitly changed with tests and evals.

## Commands

- Thread tests: `go test -v ./threads/...`
- Thread evals: `GOEVALS=1 go test -v ./threads -run Eval`

## Gotchas

- Do not inline post/thread formatting; add or reuse `format/` helpers.
- Prompt changes should be checked with targeted evals.
