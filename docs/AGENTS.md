# docs/AGENTS.md

Scoped instructions for human-facing documentation. Root rules in `/AGENTS.md` still apply.

## Audience

- `admin_guide.md`: administrators configuring the plugin.
- `user_guide.md` and `usage_tips.md`: end users.
- `providers.md`, `aws_bedrock_setup.md`, and `sovereign_ai.md`: provider setup.
- `load-testing.md`: operator-facing load testing.
- `features/*.md`: focused feature docs.

## Conventions

- Keep docs human-facing; agent work instructions belong in `AGENTS.md`.
- Match config field names and defaults to `config/` and admin UI code.
- Keep admin docs centered on System Console and plugin config APIs.
- Keep user docs free of implementation details unless needed for behavior.
- Use `snake_case.md` for new Markdown files.
- Link new feature docs from the relevant index or README section.

## Gotchas

- OpenTelemetry docs must stay in sync with `dev/docker-compose.otel.yml`.
- Load testing implementation details belong in `/loadtest/AGENTS.md`; operator steps belong here.
- Do not update localized docs or translation files outside English unless explicitly requested.

## Checks

- For docs-only changes, inspect rendered Markdown when the change is structural.
- For config docs, run or inspect the related code/tests that define the config contract.
