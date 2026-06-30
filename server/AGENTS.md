# server/AGENTS.md

Plugin entrypoint, activation/lifecycle wiring, and the data layer it depends on (`mmapi/`, `store/`, `telemetry/`, `metrics/`). Root `/AGENTS.md` applies.

## Activation order matters (`server/main.go`)

DB migrations (cluster mutex `ai_db_migrations`) → config migration/load → `bots.EnsureBots` → embedding-search init → MCP/OAuth (needs SiteURL) → `api.New`. Many services register `RegisterUpdateListener` on `config.Container`.

- **SiteURL:** `OnActivate` returns an error and the plugin won't activate if Mattermost `ServiceSettings.SiteURL` is empty. (Embedded MCP server *construction* failures are logged and non-fatal, but the missing-SiteURL check aborts activation before that point.)
- Search is held in `currentSearch atomic.Pointer[…]` and read via `getSearch()`; a model/dimension mismatch nils it until a reindex updates the stored model info (the config listener re-inits — not a deadlock).
- Cluster events (`cluster_events.go`): `config_update` reloads DB config into `Container`; `agent_update` calls `bots.ForceRefreshOnNextEnsure()`; `stream_stop` cancels streams across the HA cluster.
- `applyTelemetryConfig()` hot-reloads `TelemetryOutput` / `OpenTelemetryEndpoint` on config change (no restart).

`server/configuration.go` is only the migration-time `config.json` wrapper, not a live config adapter.

## mmapi/

`mmapi.Client` is a testable interface over `pluginapi`; mock with the generated `mmapi/mocks` (mockery), don't hand-roll. `KVGet` maps a missing key to `mmapi.ErrKVNotFound` (use `mmapi.IsKVNotFound(err)`). `mmapi.NewDBClient` is **Postgres-only** (panics otherwise) and shares the master DB with `store.New`.

## store/

Tables are prefixed `Agents_` (`Agents_ConfigHistory`, `Agents_UserAgents`, `Agents_System`, …). Schema migrations are embedded SQL under `store/migrations/`; `RunMigrations()` expects a caller-held cluster mutex. Config save is a transactional deactivate-old + insert-new serialized by an advisory lock. `GetSystemValue`/`SetSystemValue` (on `Agents_System`) are distinct from pluginapi KV.

## telemetry/ & metrics/

Reuse attribute keys from `telemetry/attributes.go`; `telemetry.Tracer()` is the helper; detach background work with `telemetry.DetachContext(ctx)`. Metrics use the `agents_*` namespace (`metrics/metrics.go`); new counters extend the `Metrics` interface and `NewMetrics` registration.
