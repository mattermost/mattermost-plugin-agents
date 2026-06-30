# bifrost/AGENTS.md

Scoped instructions for the Bifrost LLM gateway. Root rules in `/AGENTS.md` and `/llm/AGENTS.md` apply.

## Architecture

- `bifrost.LLM` is the production `llm.LanguageModel` implementation.
- Plugin wiring should go through `NewFromServiceConfig(service, bot, fallbacks)` except in tests/evals.
- OpenAI-compatible services use custom provider slots; local fallbacks must preserve chat-only downgrade behavior.
- Fallback services use their own API key and URL; never reuse primary credentials implicitly.
- `NewEmbeddingProvider` is the production embedding provider used by search wiring.
- The OTel adapter in `tracer.go` forwards Bifrost spans into plugin telemetry; do not replace it with a no-op in production.
- Provider errors and span attributes must not expose API keys.

## Commands

- Unit tests: `go test -v ./bifrost/...`.
- Focus fallback/config tests: `go test -v ./bifrost/ -run TestNewFromServiceConfig`.
- Full repo unit gate: `make test`.

## Context notes

- Admin utilities such as model listing and transcription may create their own bounded background contexts.
- Request-scoped chat/embedding paths should preserve the caller's context.
