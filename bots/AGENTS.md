# bots/AGENTS.md

Bot/agent lifecycle, LLM construction, and access control. Root `/AGENTS.md` applies.

## Lifecycle

- `MMBots.EnsureBots()` runs under cluster mutex `ai_ensure_bots` and short-circuits when config is unchanged (`botConfigsEqual` + `serviceConfigsEqual`) — the HA fast path.
- DB-backed agents are **not** part of the config-file equality check, so call `ForceRefreshOnNextEnsure()` after any agent CRUD.
- Duplicate bot **names** are fatal; an invalid service skips that bot; config bots removed from config are deactivated unless an active DB agent owns the username.

## LLM construction (`getLLM` / `getBaseLLM`)

- `getBaseLLM` returns `loadtest.NewMockLLM` for `ServiceTypeLoadTestMock`, otherwise `bifrost.NewFromServiceConfig(service, botCfg, fallbackServices)`.
- Wrappers are applied outside-in (see `llm/AGENTS.md` for the ordering rule). Anything intercepting `ChatCompletionNoStream` must wrap outside `TokenUsageLoggingWrapper`.
- Use `bot.LLM()` / `bot.GetService()` — not raw `cfg.ServiceID` on a `Bot` (resolved at ensure time).

## License & multi-bot

- File-config bots are capped to **1** when not `IsMultiLLMLicensed()`.
- DB-backed user agents (`agentStore.ListAgents()`) bypass that cap and are gated at the API via `PermissionManageOwnAgent`.

## Access control (`permissions.go`)

- `CheckUsageRestrictions` = user + channel checks; wrap failures as `ErrUsageRestriction`.
- `UsageRestrictionsForUserConfig` is the shared source of truth for both config bots and DB agents.
- Channel levels: `ChannelAccessLevelAll|Allow|Block|None` (+ `ChannelIDs`). User levels: `UserAccessLevelAll|Allow|Block|None` (+ `UserIDs`/`TeamIDs`).
- Lookups: `GetBotByID`, `GetBotByUsername`, `GetBotForDMChannel`, `GetBotMentioned` (markdown-aware, `mentions.go`).

## Transcription & native web search

- `GetTranscribe()` is separate from the chat LLM (uses `config.GetTranscriptGenerator()`; Bifrost transcriber, OpenAI/Azure only).
- `Bot.HasNativeWebSearchEnabled()` requires both `bifrost.SupportsNativeTools(service.Type)` and `"web_search"` in `EnabledNativeTools`.

## Testing

`bots_test.go`, `permissions_test.go`, `agents_test.go`; use existing `SetBotsForTesting` / `SetLLMForTest` helpers. No new mocking libraries.
