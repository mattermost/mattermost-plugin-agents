# webapp/src/components/system_console/AGENTS.md

Scoped instructions for the System Console configuration UI. Root and `webapp/AGENTS.md` rules still apply.

## Architecture

- `config.tsx` is registered as the custom admin setting named `Config`.
- Load and save through `getPluginConfig` and `savePluginConfig`; do not patch Mattermost server config.
- Persist through `registerSaveAction` / `unRegisterSaveAction`; call `setSaveNeeded()` when draft state changes.
- Reuse `Panel`, `ItemList`, `TextItem`, `BooleanItem`, and `SelectionItem`.
- Aggregate config types in `plugin_config_types.tsx`; sub-panels may export their local config types.
- Go nil slices can arrive as JSON `null`; default with `?? []` or `|| []` in UI code.

## MCP and jobs

- MCP seed/tool config flows use vetted tool constants and policy enums that must stay aligned with Go config.
- Embedding reindex UI uses job polling hooks and client endpoints; keep loading/error states visible.
- License messages use `license.tsx` and existing Enterprise/Professional chip patterns.

## Commands

- Focused admin UI tests: `cd webapp && npm run test -- --testPathPattern=components/system_console`
- Full webapp checks: `make check-style && make test`

## Gotchas

- `e2e/helpers/api-config.ts` intentionally mirrors admin config DTOs instead of importing webapp sources.
- React-select portal stacking has local styling; do not replace it with inline DOM styles.
