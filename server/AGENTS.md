# server/AGENTS.md

Scoped instructions for the plugin binary and lifecycle. Root rules in `/AGENTS.md` apply.

## Scope

- `server/` wires services; most feature code lives in root packages.
- `configuration.go` is the legacy config adapter; runtime config state lives in `config.Container` and `store/`.

## Activation order

- Initialize plugin DB store and run Morph migrations under the cluster mutex.
- Migrate old `config.json` into DB under its own cluster mutex.
- Load active config from DB, then wire bots, API, conversations, search, MCP, telemetry, and hooks.
- Preserve this ordering when moving service construction.

## Commands

- Unit tests: `go test -v ./server/...`.
- Build plugin server: `make server`.
- Deploy to a running Mattermost: `make deploy`.

## Gotchas

- Embedded MCP needs Mattermost `SiteURL` and receives runtime services directly.
- Cluster events invalidate config, agents, MCP OAuth, and stream-stop state.
- Meetings can use system `ffmpeg` or the bundled plugin path.

## Pointers

- Config schema: `/config/AGENTS.md`.
- Store/migrations: `/store/AGENTS.md`.
- Admin config behavior: `docs/admin_guide.md`.
