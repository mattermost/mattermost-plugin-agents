# i18n/AGENTS.md

Scoped instructions for server-side localization files. Root rules in `/AGENTS.md` apply.

## Catalog split

- Webapp English catalog: `webapp/src/i18n/en.json`; never hand-edit it.
- Webapp strings are added at TSX call sites and extracted with `make check-style-fix` or `make i18n-extract`.
- Server English defaults live at Go call sites passed to `T(...)`.
- Server `i18n/en.json` is translator reference data; keep it aligned only when server-side localization changes require it.
- Server non-English catalogs are embedded by `i18n/i18n.go`.

## Commands

- Webapp extraction/drift check: `make check-i18n`.
- Full style/extraction pass: `make check-style-fix`.
- Server i18n unit tests: `go test -v ./i18n/...`.

## Never do

- Do not edit language files outside US English.
- Do not use stale `webapp/i18n/en.json`; canonical webapp path is `webapp/src/i18n/en.json`.
