# releasenotes

Release tooling for Mattermost Agents. One Go program (part of the root module,
stdlib only) with three commands:

| Command | What it does | Uses an LLM |
| --- | --- | --- |
| `notes` | Writes the GitHub release notes for a commit range from the PR titles and descriptions. | yes |
| `announce` | Turns the release notes into a short Mattermost post and sends it to an incoming webhook. | no |
| `blog` | Drafts a marketing blog post from the release notes and the docs. | yes |

`notes` is wired into the release build. `announce` and `blog` are in place but
do nothing until you enable them (see [Variables and secrets](#variables-and-secrets)).

## How the release notes are generated

`mattermost/actions/plugin-ci/build` writes `dist/release-notes.md` on every
build: the line `Supported Mattermost Server Versions: **X+**`, an empty
`## Enhancements` section, and every commit subject dumped under `## Fixes`.
Mattermost's release pipeline publishes that file as the body of the GitHub
release, taken from the `all-plugin-artifacts` artifact of the `build` job in
`.github/workflows/ci.yml`.

On tag builds (`v*`) the `build/release-notes` step of that job then runs
`go run ./build/releasenotes notes --output dist/release-notes.md`, which:

1. Finds the previous release the same way the shared script does: for a full
   release `vX.Y.Z`, the newest full-release tag that is an ancestor of the
   release commit; otherwise the newest tag of any kind.
2. Lists the commits in `previous..release` and extracts PR numbers from
   `(#1234)` subjects.
3. Fetches each PR's title, description, labels and `release-note` block from the
   GitHub REST API. HTML comments (the PR template instructions), images and
   empty template headings are removed, and each description is capped at 3000
   characters (lower for very large releases, so the prompt stays under about
   300,000 characters). Commits without a PR are sent by subject only.
4. Sends everything to the Anthropic Messages API with a prompt that fixes the
   style: sections in the order `## Breaking changes and upgrade notes`,
   `## New features`, `## Improvements`, `## Fixes`; empty sections omitted; one
   terse line per item that states the user or admin-visible effect and ends with
   its PR link; no emoji, exclamation marks or marketing words; no intro or
   summary; internal changes (CI, lint, tests, refactors, docs-only,
   dependency bumps without user impact) left out; experimental features marked
   "(experimental)".
5. Checks the answer in code (`validate.go`). It is rejected if it has an emoji,
   an exclamation mark, a marketing word or first person, anything other than
   the four headings in order, text before the first heading, a line under a
   heading that is not a single `- ` item, an empty section, or an item that does
   not end with link(s) to PRs or commits that were in the input. On rejection the
   model gets the list of problems and one more attempt.
6. Writes the file atomically (temporary file, then rename) with the original
   `Supported Mattermost Server Versions: **X+** ` first line, read from
   `plugin.json` at the release commit, followed by the notes. The first line is
   produced in code, not by the model, and is byte-for-byte what
   `release-notes.sh` writes.

If a release has nothing user-visible the notes say "No user-visible changes in
this release."

### Fallback

The tool exits non-zero without touching `dist/release-notes.md` on any error: no
`ANTHROPIC_API_KEY`, a GitHub or Anthropic API error (transient 429/5xx
responses are retried three times), a timeout, a truncated or refused response,
or output that fails validation twice. The workflow step has
`continue-on-error: true`, so the build, the tarballs and the plain commit list
from `plugin-ci/build` are released exactly as before. Look for the failed
`build/release-notes` step in the tag build to see why.

## Previewing notes without releasing

Run the **release-notes-preview** workflow (Actions tab, "Run workflow") with a
tag such as `v2.11.0`. It uses the history and `plugin.json` of that tag, the code
and prompt of the branch you run it from, and shows the notes in the job summary
(rendered, plus a copyable markdown block) and as an artifact. Nothing is
released or changed. The optional `base` input overrides the previous release.

To iterate on the prompt without the model, `PRINT_PROMPT=1 make release-notes
BASE=... HEAD=...` prints exactly what would be sent.

## Running locally

```sh
export ANTHROPIC_API_KEY=...        # required
export GITHUB_TOKEN=...             # optional, raises the GitHub API rate limit
make release-notes BASE=v2.10.0 HEAD=v2.11.0
```

`BASE` defaults to the previous release before `HEAD`, and `HEAD` to `HEAD`.
Other variables: `TAG` (the tag being released, if `HEAD` is not tagged),
`OUTPUT` (a file; default stdout), `PRINT_PROMPT=1`. Use `go run
./build/releasenotes <notes|announce|blog> -h` for all flags.

## Release announcement (not enabled)

`.github/workflows/release-announce.yml`, job `announce`. On `release:
published` (or by hand with a `tag`), it reads the release body, builds this post
and sends it to `MATTERMOST_RELEASE_WEBHOOK_URL`:

```
#### Mattermost Agents v2.12.0 released

Requires Mattermost Server 12.0.0+.

**Breaking changes and upgrade notes**
- ...

**New features**
- ...

[Release notes and downloads](https://github.com/mattermost/mattermost-plugin-agents/releases/tag/v2.12.0) | [Documentation](...)
```

Only the breaking-changes and new-features sections are included; with none,
the post says so and points to the release notes. No LLM is involved. If
`MATTERMOST_RELEASE_WEBHOOK_URL` is unset the tool prints the post and exits 0.
Pre-releases and `latest-master` are skipped.

Try it locally with any notes file:

```sh
go run ./build/releasenotes announce --tag v2.11.0 --notes dist/release-notes.md
```

## Draft blog post (not enabled)

`.github/workflows/release-announce.yml`, job `blog-draft`. It gives the model
the release notes and `docs/user_guide.md`, `docs/admin_guide.md`,
`docs/mcp_server.md` and `docs/personal_agent.md` (from the release tag; missing
files are skipped) and asks for a Markdown draft: a title, a one-paragraph
summary, and one section per notable feature saying what it does, who it is for
and how to enable it with a documentation link. The same style rules apply, with
prose allowed; the draft may only link to the release, the docs URL and the docs
files it was given. The draft is written to the job summary and uploaded as the
`blog-draft-<tag>` artifact. Nothing is published and no issue or PR is opened.

## Variables and secrets

Set these under Settings, Secrets and variables, Actions.

| Name | Kind | Used by | Meaning |
| --- | --- | --- | --- |
| `ANTHROPIC_API_KEY` | secret | notes, blog | Anthropic API key. Already used by other workflows. |
| `RELEASE_NOTES_MODEL` | variable | notes, blog | Model id. Falls back to `CLAUDE_MODEL`, then `claude-sonnet-5-5`. |
| `RELEASE_ANNOUNCE_ENABLED` | variable | announce | Set to `true` to turn on the announcement job. |
| `MATTERMOST_RELEASE_WEBHOOK_URL` | secret | announce | Incoming webhook URL of the channel to post in. |
| `RELEASE_BLOG_DRAFT_ENABLED` | variable | blog | Set to `true` to turn on the blog draft job. |
| `RELEASE_DOCS_URL` | variable, optional | announce, blog | Documentation link used in both. Default: the repository `docs` folder on GitHub. |

Environment only: `RELEASE_NOTES_EFFORT` (optional `output_config.effort` value
for the model), `ANTHROPIC_BASE_URL` and `GITHUB_API_URL` (API endpoints,
mainly for tests).

## Tests

`go test ./build/releasenotes` covers PR number extraction, release range
selection against a real git history, PR body cleaning, prompt building, output
validation and emoji detection, the API clients against `httptest` servers, the
announcement format, and the notes command end to end including the "file is left
untouched on failure" behavior.
