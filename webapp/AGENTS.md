# webapp/AGENTS.md

Scoped instructions for the React/TypeScript webapp in `webapp/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Async loaders ignore responses from superseded requests (track the latest request, or cancel on input change and unmount) so a stale response never overwrites newer state.
- Use `@mattermost/compass-ui` components (buttons, icon buttons, tabs, radio groups, modals, menus) instead of hand-rolled interactive elements; anything clickable is keyboard-operable and has an accessible label.
