# customprompts/AGENTS.md

Scoped pointer for DB-backed custom prompts. Root rules in `/AGENTS.md` still apply.

- Full prompt guidance lives in `/llm/AGENTS.md`.
- Render user-authored templates through whitelisted `llm.Context.CustomPromptVars()`.
- Use the store on `mmapi.DBClient`; this is not part of `store.Store`.
- Tests: `go test -v ./customprompts/...`
