# webapp/AGENTS.md

Scoped instructions for the Mattermost plugin webapp bundle. Root rules in `/AGENTS.md` still apply.

## Architecture

- `src/index.tsx` defines the plugin class registered through `window.registerPlugin`; Mattermost registrations happen in `initialize(registry, store)`.
- React, Redux, react-intl, and related libraries are webpack externals provided by the Mattermost webapp.
- `src/mm_webapp.ts` bridges host exports from `window.Components` and product APIs. Guard feature availability with helpers instead of assuming a host version.
- Plugin Redux state lives under `plugins-${manifest.id}`.
- Put plugin REST calls in `src/client.tsx` with `${Client4.url}/plugins/${manifest.id}/...` after `setSiteURL()`.
- Normalize API quirks at the client boundary, such as null turn content to `[]`.

## User-facing surfaces

- Agents product: `components/agents/agents_page.tsx`.
- RHS: `components/rhs/`.
- Streaming bot posts: `components/llmbot_post/`.
- Admin config: `components/system_console/`; read its nested `AGENTS.md`.

## Conventions

- Use the `@/` alias for `src/` imports.
- Use styled-components for layout and visual styling. Prefer Mattermost CSS variables.
- Inline `style={{...}}` is only for existing dynamic-position or host-passed style cases.
- Use `FormattedMessage` or `useIntl().formatMessage` with `defaultMessage` at the call site.
- Keep backend-aligned types in `src/types/` synchronized with Go contracts.
- Websocket event names use the `custom_mattermost-ai_*` prefix.

## Commands

- Webapp lint/typecheck via root: `make check-style`
- Lint fix + i18n extract: `make check-style-fix`
- i18n drift check: `make check-i18n`
- Single Jest file: `cd webapp && npm run test -- --testPathPattern=path/to/file.test`
- Typecheck only: `cd webapp && npm run check-types`
- Bundle: `make webapp`

## Testing

- Co-locate `*.test.ts(x)` beside source.
- Mock existing boundaries such as `@/client`, `react-redux`, `react-intl`, and `@/mm_webapp`; do not add new mocking libraries.
- For UI behavior changes, also consider Playwright coverage in `e2e/`.

## Never do

- Never edit `webapp/dist/` or generated `webapp/src/manifest.ts`.
- Never hand-edit `webapp/src/i18n/en.json`.
- Never patch Mattermost server config for plugin settings; use the admin config API.
