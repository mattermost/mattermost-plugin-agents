# releasenotes

Writes the GitHub release notes for a Mattermost Agents release with an LLM,
from the titles and descriptions of the pull requests in the release.

## How it runs

`mattermost/actions/plugin-ci/build` writes `dist/release-notes.md` on every
build: the line `Supported Mattermost Server Versions: **X+**` followed by every
commit subject. Mattermost's release pipeline publishes that file as the body of
the GitHub release, taken from the `all-plugin-artifacts` artifact of the
`build` job in `.github/workflows/ci.yml`.

On tag builds (`v*`) the `build/release-notes` step of that job then runs
`go run ./build/releasenotes --output dist/release-notes.md`, which:

1. Finds the previous release with `git describe`: the previous full release
   for a full release `vX.Y.Z`, otherwise the previous tag of any kind.
2. Lists the commits in `previous..release` and reads the PR number from the
   `(#1234)` at the end of each squash-merge subject.
3. Fetches each PR with `gh api`. HTML comments (the PR template instructions)
   are removed and each description is capped at 3000 characters. PRs whose
   author has no write access (not an owner, member or collaborator) are sent
   by commit subject only, because a PR's title and description can be edited
   after merge without review.
4. Sends everything to the Anthropic Messages API. The system prompt in
   `notes.go` sets the format and style: sections `## New features`,
   `## Breaking changes and upgrade notes`, `## Improvements`, `## Fixes`; one
   or two short sentences per item ending with its PR link; no emoji or
   marketing language; internal changes left out.
5. Writes the file with the original `Supported Mattermost Server Versions`
   line, read from `plugin.json` at the release commit, followed by the notes.

The output is not checked. To change the notes, change the prompt.

On any error (no `ANTHROPIC_API_KEY`, a `git`, `gh` or API failure) the tool
exits non-zero without touching `dist/release-notes.md`. The step has
`continue-on-error: true`, so the plain commit list is released as before.

## Previewing notes without releasing

Run the **release-notes-preview** workflow (Actions tab, "Run workflow") with a
tag such as `v2.11.0`. It uses the history and `plugin.json` of that tag and the
code and prompt of the branch you run it from, and shows the notes in the job
summary. The optional `base` input overrides the previous release.

## Running locally

Needs `ANTHROPIC_API_KEY` and the `gh` CLI, logged in or with `GITHUB_TOKEN` set.

```sh
make release-notes BASE=v2.10.0 HEAD=v2.11.0
PRINT_PROMPT=1 make release-notes HEAD=v2.11.0   # print the prompt, no model call
```

`BASE` defaults to the previous release before `HEAD`, and `HEAD` to `HEAD`.
Also `TAG` (the tag being released, if `HEAD` is not tagged) and `OUTPUT` (a
file; default stdout). The model is `RELEASE_NOTES_MODEL`, default
`claude-sonnet-5-5`; in CI it falls back to the `CLAUDE_MODEL` variable.
`ANTHROPIC_BASE_URL` overrides the API endpoint.
