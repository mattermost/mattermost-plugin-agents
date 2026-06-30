# build/AGENTS.md

Scoped instructions for build, manifest, deploy, and release helper tooling. Root rules in `/AGENTS.md` still apply.

## Manifest workflow

- `build/manifest/` reads `plugin.json` and generates manifest-derived files.
- Generated files include `server/manifest.go` and `webapp/src/manifest.ts`.
- Run `make apply` after manifest changes; never hand-edit generated manifest outputs.
- `make check-style` includes manifest validation and apply checks.

## Pluginctl

- `build/pluginctl/` deploys, enables, disables, resets, and tails plugin logs.
- `make deploy` builds a bundle and deploys it through pluginctl.
- Local socket settings come from Mattermost dev environment variables or defaults.

## FIPS and forks

- `build/fips.mk` enables opt-in FIPS builds; do not enable casually.
- `build/custom.mk` is the extension hook for forks.

## Commands

- Manifest check: `make manifest-check`
- Apply manifest data: `make apply`
- Build distribution: `make dist`
- CI/cloud bundle: `make dist-ci`
- Deploy: `make deploy`
- Plugin control: `make pluginctl`
- Copyright headers: `make copyright`

## Gotchas

- `build/bin/` is generated and gitignored.
- Do not edit generated distribution assets in `dist/`, `server/dist/`, or `webapp/dist/`.
