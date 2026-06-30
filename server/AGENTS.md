# server/AGENTS.md

Scoped instructions for plugin lifecycle and composition. Root rules in `/AGENTS.md` still apply.

## Architecture

- Treat `main.go` as the composition root. Wire services there; keep business logic in feature packages.
- Activation order matters: store migrations, config migration, bots/prompts, streaming, search/indexer, MCP, conversations, `conversation.Service`, meetings, policy checker, then API.
- Setter-based cycle breakers are intentional: `conversations.SetMeetingsService`, `search.SetConversationService`, and `api.SetConversationService`.
- Embedding search is held in `atomic.Pointer[embeddings.EmbeddingSearch]`; config changes may set it to nil until model compatibility is restored by reindex.
- SiteURL is required for embedded MCP startup.
- Telemetry is initialized on activation and config changes; shut it down on deactivation.

## Commands

- Server tests: `go test -v ./server/...`
- Plugin deploy: `make deploy`

## Gotchas

- Bot ensure failures are logged so admins can fix config; they do not always fail activation.
- Keep schema migrations and config migration cluster mutexes separate.
- Do not introduce production `context.Background()` shortcuts in service wiring.
