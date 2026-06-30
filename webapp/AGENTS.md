---
description: React/TypeScript Mattermost plugin bundle — webpack externals, plugin registry, Redux namespace, FormatJS i18n scope.
tags: [webapp, react, typescript, redux, styled-components, i18n, jest]
---

# webapp/AGENTS.md

Mattermost-plugin-embedded React/TypeScript bundle. Not a standalone app — there is no dev server; iterate with `make watch` or `make deploy`. Root conventions (PascalCase components, strict TS, styled-components only, snake_case filenames, never edit `dist/`) still apply.

## Commands

- `make apply` **first** — generates the gitignored `src/manifest.ts` from root `plugin.json`. Without it, `@/manifest` imports fail.
- From `webapp/`: `npm run lint` / `npm run fix` (ESLint), `npm run check-types` (`tsc`), `npm run test` (Jest). Pre-PR gate is root `make check`.
- Node **24.11** (`.nvmrc`). Dev loop: `make watch` (rebuilds + auto-`deploy-from-watch`); `MM_DEBUG=1` for dev bundles.

## Structure (file localization)

- Single webpack entry **`src/index.tsx`** → single bundle `dist/main.js`. `Plugin.initialize(registry, store)` registers all hooks.
- All server calls live in **`src/client.tsx`** (`Client4.getOptions()` against `/plugins/${manifest.id}/…`). Mirror new endpoints from `api/` here.
- Redux: plugin state is namespaced at **`state['plugins-' + manifest.id]`**. Register reducers in `redux.tsx`; read via `selectors.ts`.
- **`hooks.tsx` ≠ `hooks/`**: the file has dispatch helpers; the directory has React hooks plus module-level conversation caches invalidated by websocket events (`index.tsx`). Don't confuse them.
- Feature areas: RHS chat → `components/rhs/`, admin UI → `components/system_console/`, Agents product page → `components/agents/`, LLM posts → `components/llmbot_post/`.

## Conventions

- **i18n:** use `FormattedMessage` / `formatMessage({defaultMessage})` (ESLint `jsx-no-literals` forbids raw JSX strings). Run `make i18n-extract` / `make check-style-fix`. **Never hand-edit `src/i18n/en.json`.** Extraction scans only `src/index.tsx` + `src/components/**` — put user-facing strings there or extend the Makefile glob. `es.json` is a manually maintained partial keyed by the same hash IDs (English only per repo rule — leave it for translators).
- **Styling:** styled-components + Mattermost CSS vars (`var(--center-channel-*)`); no local `ThemeProvider`. Avoid inline `style={{}}` (ESLint won't catch it). 4-space indent, single quotes.
- **Externals:** React/ReactDOM/Redux/react-redux/react-intl/react-bootstrap/react-router-dom are provided by the Mattermost host (`webpack.config.js`), not bundled. Runtime also depends on `window.Components`, `window.PostUtils`, `window.ProductApi` (`mm_webapp.ts`).
- **Types in `types/conversation.ts` and `types/agents.ts` must match the Go backend** (block types, tool statuses, snake_case JSON fields) — check the comments before changing constants.

## Tests

Jest + ts-jest + jsdom. Co-locate `*.test.ts(x)` next to source; shared setup in `webapp/tests/`. Common mocks: `react-intl` (babel FormatJS IDs aren't applied under Jest), `@/client`, `window.PostUtils`, and `react-bootstrap` (virtual mock, since it's a webpack external).
