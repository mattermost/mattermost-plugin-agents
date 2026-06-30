# docs/AGENTS.md

Scoped instructions for user, admin, feature, and operator documentation. Root rules in `/AGENTS.md` still apply.

## Edit the right doc

| Trigger | Primary file | Also review |
| --- | --- | --- |
| Install, System Console, MCP, telemetry, license matrix | `admin_guide.md` | `providers.md` |
| End-user RHS/composer/thread workflows | `user_guide.md` | Relevant `features/*.md` |
| New user-facing feature | `features/<feature>.md` | `user_guide.md`, README doc index |
| Provider setup or model IDs | `providers.md` | `aws_bedrock_setup.md`, `admin_guide.md` |
| Upgrade or migration behavior | `upgrading_to_2.0.md` | `features/managing_agents.md` |
| Tool approval or channel privacy | `features/multiplayer_tool_calling.md` | `user_guide.md`, `admin_guide.md` |
| Load testing | `load-testing.md` | `loadtest/` and `loadtest/controller/` |

## Voice and wording

- User docs are task-oriented and use "you".
- Admin docs use imperative setup steps and call out security tradeoffs.
- Match exact UI labels in bold.
- Verify webapp labels against `webapp/src/i18n/en.json` or source `defaultMessage` values.
- Verify server/plugin strings against root `i18n/en.json`.
- Prefer current v2 navigation:
  - Plugin-wide settings: **System Console > Plugins > Agents**
  - Agent CRUD: Agents product page or product switcher -> **Agents**
- Document config keys and defaults when admins might set them through the plugin admin config API.

## Assets and links

- Store doc UI images in `docs/img/`.
- Reference images from `docs/*.md` as `img/...`; from `docs/features/*.md` as `../img/...`.
- Alt text should describe the visible UI state and important labels.
- Use relative `.md` links for in-repo docs.
- When adding a feature doc, update the README Documentation section.
- New `docs/features/*.md` files should match the copyright header used by sibling feature docs.

## Review and testing

- There is no docs-specific CI target.
- For docs-only changes, manually verify links, anchors, screenshots, and quoted UI labels.
- For UI behavior changes, update docs and Playwright coverage together when the documented flow changes.
- For provider/config changes, cross-check `providers.md`, `admin_guide.md`, `config/`, and `llm/`.

## Never do

- Never hand-edit `webapp/src/i18n/en.json` for docs.
- Never document a feature as generally available without checking license/experimental status.
- Never use `docs/` for developer setup that belongs in `README.md` or scoped package docs.
- Never leave stale System Console paths after UI moves.
