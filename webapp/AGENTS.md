# webapp/AGENTS.md

Scoped instructions for `webapp/`. Root rules in `/AGENTS.md` still apply; only webapp-specific rules live here.

## Layout

- `src/index.tsx` is the plugin entrypoint and registers RHS, product pages, post types, admin settings, and websocket handlers.
- `src/client.tsx` is the plugin HTTP client for `/plugins/mattermost-ai/...`.
- `src/mm_webapp.ts` wraps Mattermost webapp exports; feature registration can depend on host compatibility helpers.
- `src/components/rhs/`, `src/components/agents/`, `src/components/system_console/`, and `src/components/llmbot_post/` are the main UI surfaces.
- `src/i18n/` holds webapp catalogs; `dist/` is webpack output and is never edited.

## Prerequisites

- Node version comes from repo `.nvmrc`.
- Run `make apply` or a Make target that depends on it before build/test; it generates gitignored `src/manifest.ts`.

## Commands

- Pre-PR webapp slice: `make check-style test check-i18n check-locks`.
- Lint + typecheck: `make check-style` (`npm run lint` + `npm run check-types`).
- Auto-fix + i18n extraction: `make check-style-fix`.
- Unit tests: `make test` or, from `webapp/`, `npm run test`.
- Single Jest test: `cd webapp && npm run test -- --testPathPattern=path_or_name`.
- Production bundle: `make webapp` or, from `webapp/`, `npm run build`.
- Dev watch + deploy loop: `make watch`.

## Conventions

- File names are `snake_case.ts(x)`; React components are PascalCase.
- Use styled-components for styling; do not add inline `style={{...}}`.
- Use strict TypeScript and the `@/` alias for cross-tree imports.
- User-facing strings use `FormattedMessage` or `useIntl` with `defaultMessage`.
- For discriminated unions, switch statements must include a `never` exhaustiveness check.
- Mattermost host libraries are webpack externals; do not bundle them.

## i18n

- Never hand-edit `src/i18n/en.json`; add or update strings at call sites and run `make check-style-fix` or `make i18n-extract`.
- Extraction covers `src/index.tsx` and `src/components/**/*.{ts,tsx}`.
- `es.json` is partial and hand-maintained; do not auto-generate it.
- Do not confuse webapp catalogs with server catalogs in repo-root `i18n/`.
- Jest may need `IntlProvider` wrappers or existing `react-intl` mocks because formatjs IDs are added by Babel.

## Tests

- Jest + Testing Library tests are colocated as `*.test.ts(x)`.
- Mock Mattermost globals and host components using existing test patterns; do not add a mocking library.
- UI flows spanning the plugin and Mattermost shell belong in `e2e/`.

## Never do

- Never edit `dist/` or `src/manifest.ts`; regenerate them.
- Never hand-edit `src/i18n/en.json`.

## Pointers

- Admin UI behavior and config fields: `docs/admin_guide.md`.
- Agents product page behavior: `docs/features/managing_agents.md`.
- User-facing chat/tool flows: `docs/user_guide.md`.
- Playwright coverage and shards: `e2e/AGENTS.md`.
