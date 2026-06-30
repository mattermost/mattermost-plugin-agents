# bots/AGENTS.md

Scoped instructions for Mattermost bot users, agents, and LLM wrapper wiring. Root rules in `/AGENTS.md` still apply.

## Bot model

- `Bot` runtime fields are resolved during ensure. Use `GetService()` instead of reading `cfg.ServiceID` directly after construction.
- Duplicate bot usernames are fatal.
- DB-backed agents and file-configured bots share registry behavior but differ in license/quota enforcement.

## EnsureBots

- `EnsureBots()` uses optimistic config equality plus a cluster mutex.
- Call `ForceRefreshOnNextEnsure()` after DB-backed agent changes so config snapshots do not hide registry updates.
- Indexer and DM detection rely on `IsAnyBot`, `GetBotForDMChannel`, and `GetAllBotUserIDs`.

## LLM wrappers

- Wrapper order is set here; be careful with wrappers that convert non-stream calls into streaming calls.
- Token usage logging and structured-output fallback behavior must remain compatible with `llm/AGENTS.md`.

## Web search and transcription

- `HasNativeWebSearchEnabled()` requires config enablement and provider native-tool support.
- Built-in web search fallback should be suppressed when native web search is available.
- `GetTranscribe()` provides the transcription bot used by meetings.

## Commands

- Bot tests: `go test -v ./bots/...`
- Agent registry focus: `go test -v ./bots/ -run TestDBBackedAgentInBotRegistry`

## Pointers

- LLM stack: `/llm/AGENTS.md`.
- Web search providers: `/websearch/AGENTS.md`.
- Meeting transcription: `/meetings/AGENTS.md`.
