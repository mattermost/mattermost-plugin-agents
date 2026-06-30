---
description: React/TypeScript plugin webapp — Mattermost registry integration, formatjs i18n, redux, styling, and Jest tests.
tags: [webapp, react, typescript, redux, styled-components, i18n, jest]
---

# webapp/AGENTS.md

React/TypeScript bundle loaded by the Mattermost webapp. Webpack compiles `src/index.tsx` → `dist/main.js` (`plugin.json` `webapp.bundle_path`). Root `/AGENTS.md` still applies.

## Commands

Run from `webapp/` (or use the root Makefile wrappers):

- Lint + types: `npm run lint` + `npm run check-types` (or root `make check-style`).
- Auto-fix + re-extract i18n: root `make check-style-fix`.
- Unit tests: `npm run test`. Single test: `npx jest --config jest.config.js src/path/file.test.tsx` or `-t "name"`.
- Build: `npm run build` (prod) / `npm run debug` (dev). Live reload from root: `make watch`.
- `make apply` regenerates `src/manifest.ts` from `plugin.json` — run before lint/build if the manifest changed.

## Key files

- `src/index.tsx` — `Plugin.initialize(registry, store)`, `registerPlugin(manifest.id, …)`.
- `src/client.tsx` — HTTP client for `/plugins/${manifest.id}/…`.
- `src/redux.tsx`, `src/selectors.ts`, `src/redux_actions.tsx` — plugin redux slice.
- `src/mm_webapp.ts` — host webapp shims (`window.Components.*`); `isRHSCompatable()` gates RHS.
- `src/components/` — UI (`rhs/`, `agents/`, `system_console/`, `llmbot_post/`, `custom_prompts/`).
- `src/i18n/en.json` — canonical extracted catalog (generated).

## Conventions & gotchas

- **Styling:** styled-components; use Mattermost CSS vars (`var(--center-channel-bg)`). Avoid inline `style={{...}}` (a few legacy exceptions exist; don't add more).
- **i18n:** strings go through `FormattedMessage` / `formatMessage`; ESLint `react/jsx-no-literals` forbids raw JSX text. Extraction (`make i18n-extract`) scans only `src/index.tsx` + `src/components/**/*.{ts,tsx}` — put user-facing strings there or they won't be picked up. **Never hand-edit `src/i18n/en.json`.** In Jest, formatjs doesn't transform; tests mock `FormattedMessage` to render `defaultMessage`.
- **Host integration:** registry APIs are feature-detected (`if (registry.registerX)`) for version compat. `react`, `react-dom`, `redux`, `react-redux`, `react-intl`, `react-bootstrap`, `react-router-dom` are webpack **externals** — never bundle them; `react-bootstrap` imports need an `import/no-unresolved` disable.
- **Redux:** plugin state lives under `plugins-${manifest.id}`; reference `manifest.id`, not a hardcoded string.
- **Type sync:** TS types mirroring Go must stay in sync (e.g. `src/types/conversation.ts` ↔ `conversation/content_block.go`, `src/utils/tool_names.ts`). Update both sides together.
- **Tests:** co-located `*.test.ts(x)`; `jsdom` + `ts-jest`; `@/` path alias maps to `src/`.
- **Generated/do-not-edit:** `dist/`, `src/manifest.ts`.

## Pointers

- Agents page UX + websockets: `docs/features/managing_agents.md`. Admin UI: `docs/admin_guide.md`.
