# webapp/AGENTS.md

Scoped instructions for the React/TypeScript webapp in `webapp/`. Root rules in `/AGENTS.md` still apply; only deviations and package-specific gotchas live here.

- Async loaders ignore responses from superseded requests (track the latest request, or cancel on input change and unmount) so a stale response never overwrites newer state.
- Use `@mattermost/compass-ui` components (buttons, icon buttons, tabs, radio groups, modals, menus) instead of hand-rolled interactive elements; anything clickable is keyboard-operable and has an accessible label.
- Spacing (padding, margin, gap) uses compass `--spacing-*` tokens, not raw px.
- Host CSS variables such as `--border-light` are whole shorthands (`border-bottom: var(--border-light)`), not colors; `1px solid var(--border-light)` is invalid.
- Plugin modals sit at z-index 2000 and their menus at 2100, above the host tooltip layer (1350). A host `WithTooltip` inside a modal renders behind it: use `TooltipAboveModals` from `@/components/dropdown`, and prefer `DotMenu` triggers (which already do).
- A modal is an `AnimatedModalShell` (scrim, Escape, exit fade) around a compass `Modal`, and the `Modal` gets `open={show}` too: without it the sheet has no enter or exit motion, only the scrim fades. Use the shell for every modal, nested ones included (a modal opened from inside another stacks above it and takes Escape).
- Popover menus animate through compass `PopoverMenu` `open`/`onExited`; a `Dropdown` must stay mounted until `onExited` (`useMenuTransition`), as `DotMenu` does. Passing no `open` renders the menu with no animation. This is plugin-side glue: compass-ui plans to provide more of it (positioning and open/close handling for menus), so check the installed compass-ui for that before extending `useMenuTransition` or `Dropdown`, and drop the glue once compass covers it.
- The `agents/*` lint rules are jsx-a11y rules that resolve styled-components declared in the same file to their DOM tag. Styled components imported from another file are invisible to them, so prefer native elements (`styled.button`) over adding handlers to imported wrappers.
