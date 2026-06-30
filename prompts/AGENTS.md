# prompts/AGENTS.md

Scoped pointer for embedded prompt templates. Root rules in `/AGENTS.md` still apply.

- Full prompt guidance lives in `/llm/AGENTS.md`.
- Templates live in `*.tmpl` files and are embedded through `PromptsFolder`.
- After adding or renaming a template, run `go generate` in this directory.
- Tests: `go test -v ./prompts/...`
