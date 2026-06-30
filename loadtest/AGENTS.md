---
description: In-process mock LLM and the nested mattermost-load-test-ng controller module.
tags: [loadtest, mock-llm, go-module]
---

# loadtest/AGENTS.md

Non-production load simulation. Two parts:

- **`loadtest/` (root module):** `MockLLM` implements `llm.LanguageModel` in-process for the `loadtest_mock` service type (no Bifrost/HTTP). Profiles are JSON (`profile.go`). Invalid `loadTestMockConfig` JSON fails at bot init.
- **`loadtest/controller/` (nested `go.mod`):** a `mattermost-load-test-ng` `SimulController` plugin that drives agent traffic via mentions/DMs. Kept as a separate module so the load-test-ng dependency stays out of the production plugin build.

Conventions:
- The nested module is invisible to root gates — changes need `make loadtest-controller-{test,lint,mod-check,build}`, and `make check-go-mods` checks its tidy state.
- Plugin id `mattermost-ai`; action names `AskAgentChannelMention` / `AskAgentDM`. Trigger frequencies are relative weights, not probabilities. MCP tools should use `auto_run_everywhere` during load tests.
- Operator guide: `docs/load-testing.md`. Don't commit local `replace` directives.
