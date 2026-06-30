# webapp/AGENTS.md

React/TypeScript bundle that runs **inside** a Mattermost server webapp (no standalone dev server). Root `/AGENTS.md` still applies.

## Commands

Use repo `make` targets for multi-step flows; use `cd webapp && npm run …` for fast iteration. npm (Node 24.11, root `.nvmrc`).

- Lint + typecheck (run by `make check-style`): `npm run lint` && `npm run check-types`
- Auto-fix lint: `npm run fix`
- All unit tests (run by `make test`): `npm run test`
- Single test file: `npm run test -- src/components/tool_card.test.tsx`
- Single test by name: `npm run test -- --testPathPattern=tool_card -t "no parameters"`
- Production bundle: `npm run build` (or `make webapp`); dev bundle with source maps: `MM_DEBUG=1 make webapp`
- Live reload into a running server: `make watch` (webpack `--watch` → `make deploy-from-watch` on each emit; plugin must already be deployed)

## Build invariants

- `webapp/src/manifest.ts` is **generated** from `plugin.json` by `make apply` and is gitignored. A fresh checkout must run `make apply` (or any target depending on it, e.g. `make check-style`) before typecheck/tests compile.
- Webpack `externals` (host-provided, not bundled): `react`, `react-dom`, `redux`, `react-redux`, `prop-types`, `react-intl`, `react-bootstrap`, `react-router-dom`. A new UI library needs an explicit `externals`/bundling decision in `webpack.config.js`.
- `babel.config.js` must stay at `webapp/` root (jest reads it there).

## Conventions

- All plugin HTTP goes through `src/client.tsx` (`@mattermost/client` `Client4`, routes under `${Client4.url}/plugins/${manifest.id}/…`). Do not call `fetch` from components/hooks.
- Plugin Redux slice lives at `state['plugins-' + manifest.id]`; register reducers via `registry.registerReducer` in `setupRedux` (`src/redux.tsx`). Action-type constants are exported from `src/redux.tsx`.
- Host integration (components/APIs that differ by Mattermost version) goes through `src/mm_webapp.ts`; gate with helpers like `isRHSCompatable()`. Post markdown uses `window.PostUtils`.
- New surfaces (post types, RHS, admin console, product route, websocket handlers) register in `src/index.tsx`. Custom websocket events use the `custom_mattermost-ai_*` prefix.
- Import aliases `@/` and `src/` both resolve to `webapp/src/` (tsconfig + jest `moduleNameMapper`).
- Styled-components only, never inline `style={{…}}` (root rule; convention, not lint-enforced here).

## i18n

- User-visible JSX uses `FormattedMessage` / `useIntl().formatMessage` (ESLint `react/jsx-no-literals`).
- `make i18n-extract` only scans `src/index.tsx` and `src/components/**`. Strings in `src/hooks/`, `src/client.tsx`, `src/commands.ts` are **not** extracted — put translatable UI strings in `components/` (or extend the Makefile glob).
- Never hand-edit `src/i18n/en.json` (regenerated). Only edit `en.json` for translations; never touch other language catalogs (`es.json`, etc.).

## Tests

Jest + `@testing-library/react` + jsdom; setup stub `tests/setup.tsx` (no full webapp boot). Mock at module boundaries — copy patterns from existing `*.test.tsx`: mock `@/client` functions (not HTTP), stub `react-redux`/`react-intl`/`@/mm_webapp`, set `window.PostUtils` in `beforeEach`. Don't assert styled-components class names or registry wiring order.

## Never

- Never edit `webapp/dist/` or generated `src/manifest.ts`.
