# i18n/AGENTS.md

Scoped instructions for server-side localization. Root rules in `/AGENTS.md` still apply.

## Two i18n systems

- `/i18n/` is server-side Go i18n using `nicksnyder/go-i18n`.
- `webapp/src/i18n/en.json` is webapp formatjs output generated from TS/TSX call sites.
- Do not edit webapp extracted JSON while working in this directory.

## Server catalog workflow

- `en.json` is hand-curated and is the only locale agents should edit unless explicitly asked otherwise.
- Keep message IDs stable and descriptive, usually dotted names.
- `i18n.go` embeds JSON catalogs and initializes the bundle.
- Use localizers from this package instead of ad hoc string selection.

## Commands

- Server i18n tests: `go test -v ./i18n/...`
- Webapp extraction check, not for this catalog: `make check-i18n`

## Gotchas

- `make i18n-extract` updates webapp strings only.
- Do not touch non-English translation files unless explicitly requested by a human.

## Pointers

- Webapp i18n: `/webapp/AGENTS.md`.
