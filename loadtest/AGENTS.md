# loadtest/AGENTS.md

In-process mock LLM for load testing. Operator setup lives in `docs/load-testing.md`. Root `/AGENTS.md` applies.

## Mock LLM

- Service type `llm.ServiceTypeLoadTestMock` (`"loadtest_mock"`) is in-process (`loadtest.NewMockLLM`), not Bifrost/HTTP. Wired in `bots/bots.go` `getBaseLLM`.
- Profile JSON is parsed by `loadtest.ParseProfile`; empty config → `DefaultReadSearchHeavyProfile()`; unknown fields are rejected. Never use `loadtest_mock` in production config.
- The mock stops emitting tool calls once `max_tool_rounds` is reached. Profile validation requires `0 <= max_tool_rounds <= 10` (`toolrunner/limits.MaxToolRounds`).

## Testing

- Core: `go test ./loadtest/... ./bots/... ./toolrunner/... -race`.
- `loadtest/controller/` is a **separate Go module** (excluded from root `go test ./...`). Run it on its own: `(cd loadtest/controller && go test ./... -race)`. Root `make check` covers it via `check-go-mods` + the `loadtest-controller-*` targets.
