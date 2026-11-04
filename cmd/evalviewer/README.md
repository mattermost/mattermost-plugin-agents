# Eval Viewer

A CLI tool to run evaluations and display results in a TUI (Terminal User Interface).

## Installation

### From Local Repository (Recommended)
```bash
cd cmd/evalviewer
go install
```

After installation, the `evalviewer` command will be available in your PATH.

## Usage

### Run Command (Recommended)
Run go test with `GOEVALS=1` environment variable set, then automatically find and display the evaluation results in a TUI.

All arguments after 'run' are passed directly to 'go test'.

```bash
# Run evaluations for conversations package
evalviewer run ./conversations

# Run all evaluations
evalviewer run -v ./...

# Run with test coverage
evalviewer run -cover ./conversations
```

The run command will:
1. Clean up any existing evals.jsonl file
2. Execute go test with GOEVALS=1
3. Search for evals.jsonl in current and parent directories
4. Launch the TUI to display results

### View Command  
Display evaluation results from an existing evals.jsonl file in a TUI.

```bash
# View existing results (defaults to evals.jsonl in current directory)
evalviewer view

# View results from specific file
evalviewer view -file evals.jsonl
evalviewer view -f /path/to/evals.jsonl

# Show only failed evaluations
evalviewer view -failures-only
```

### Check Command (CI Mode)
Run evaluations and check results without the interactive TUI. Exits with status code 1 if any evaluations fail. This is designed for CI/CD pipelines.

All arguments after 'check' are passed directly to 'go test'.

```bash
# Run and check evaluations (no TUI)
evalviewer check ./conversations

# Run and check all evaluations
evalviewer check ./...

# Run with verbose output
evalviewer check -v ./...
```

The check command will:
1. Clean up any existing evals.jsonl file
2. Execute go test with GOEVALS=1
3. Display test output in real-time
4. Print a summary of evaluation results
5. Exit with status code 1 if any evaluations failed

A package that did not run all of its evals always fails the command: it failed to build, `TestMain` exited, it panicked or timed out, or a test failed without recording a result (for example, because setting up its containers failed). Set `EVALS_REPORT_ONLY=1` to exit non-zero only in those cases and leave failed evals to `evalviewer report`. CI does this, so the eval job fails exactly when its results are partial, and partial results never become a baseline. When every package finished, the command appends a completion line to `evals.jsonl`; `report` relies on it to tell a removed eval from one that did not run. `run` and `check` add `-v` if it is missing, since unfinished tests are detected from its `=== RUN` lines.

### Report Command (CI)
Summarize existing results for a pull request. Reads `evals.jsonl` files from a directory (or its immediate subdirectories, the layout `actions/download-artifact` produces) and compares them with an optional baseline from the target branch.

```bash
evalviewer report --results eval-results --baseline eval-baseline \
  --providers openai,anthropic,azure,mistral,bedrock \
  --comment comment.md --summary summary.md
```

The report writes:
- `comment.md`: only checks that fail on this change but not on the baseline. With `--previous` (the comment already on the pull request) it also says when the earlier failures are fixed. It is empty when the existing comment should be left as it is, and CI changes nothing in that case.
- `summary.md`: every provider's status, including pre-existing failures, flaky checks, providers that could not run (missing credentials, retired models), and failures of providers that have no usable baseline while other providers do. CI adds it to the workflow run summary.

A rubric is graded from the agent's response alone, so it should check only what the response says. State the response cannot show, such as who received a DM or which tools ran, belongs in the eval's code assertions.

Besides one check per rubric, every graded run records a "Passes the eval's code assertions" check for the eval's other assertions, such as required tool calls or valid citations, so they are reported even when the rubrics pass. Each run of an eval is its own subtest (`run_0`, `run_1`, ...), so a `require` failure in one run does not skip the others.

A listed check is dropped only on evidence that it is not the change's problem. The comment lists every failing check it reports in a hidden `eval-checks` line. On a later run, a listed check is dropped when its eval ran and the check passes or also fails on the baseline, or when the run completed and the eval no longer has any result (it was removed). Any result for the eval counts as a recheck, even if the check errored earlier or its rubric was edited. A check whose eval has no result in a run that may not have run it (its provider had an outage, a package crashed) stays under "Not rechecked in this run". A check that still fails while the baseline has no results for its provider stays under "Still failing, with nothing to compare against". The comment is resolved only once nothing is left.

Repeated runs (`GOEVALS=N`) are combined by majority vote, so a check that fails one run out of three is reported as flaky rather than failing. A run that errors before grading (for example, the provider rejects the request) is recorded with its error, with credential values redacted.

### How CI uses the evals
- `make evals-ci` runs every test whose name ends in `Eval` in `conversations`, `threads`, `channels`, `react`, and `mcpserver`. The `mcpserver` evals start Mattermost containers and need Docker.
- Before the first eval, each provider and the grader get one small request. If a model is unusable (bad key, no access to the model, missing deployment), every eval for that provider is recorded with that error instead of failing one by one.
- Evals run on pull requests that change more than `webapp/`, `e2e/`, `docs/`, `loadtest/`, images, or Markdown, and on every push to `master`.
- The `evals-comment` job compares pull request results with the latest `master` results and comments only when a check newly fails. When a later push fixes them, the comment is replaced with a short resolved note. It runs even when the eval job failed, so a crashed run never looks like a fix.
- Set the `EVAL_RUNS` repository variable to run each eval several times per CI run.

### Nightly runs and history
`.github/workflows/nightly-evals.yml` runs every eval against `master` at 06:00 UTC (and on manual dispatch), three times each by default (`NIGHTLY_EVAL_RUNS` repository variable). It compares the results with the previous night and stores them in S3 under `s3://$AWS_S3_BUCKET/mattermost-plugin-agents/evals/nightly/<date>/<run id>/`, along with `summary.json` (written by `report --json`). The run summary shows the report and a 14-night trend, and the run fails when a check newly fails, so GitHub notifies the workflow's owner.

To look at history locally, download the summaries and render the trend:

```bash
aws s3 cp s3://$AWS_S3_BUCKET/mattermost-plugin-agents/evals/nightly/ eval-history \
  --recursive --exclude '*' --include '*/summary.json'
evalviewer trend --history eval-history --limit 30
```

## Environment Variables

- **`GOEVALS`**: Number of runs per eval (default: `1`). The `run`, `check`, and CI commands set it to `1` when unset.

The evalviewer and evaluation tests support multiple LLM providers. Configure which providers to use and their settings with these environment variables:

### Provider Selection

- **`LLM_PROVIDER`**: Choose which provider(s) to run evaluations with
  - Values: `openai`, `anthropic`, `azure`, `mistral`, `bedrock`, `cohere`, `openaicompatible`, `all`, or comma-separated (e.g., `openai,azure`)
  - Default: `all` (runs all providers)

### OpenAI Configuration

- **`OPENAI_API_KEY`**: Your OpenAI API key (required for OpenAI)
- **`OPENAI_MODEL`**: Model to use (overrides code default)

### Anthropic Configuration

- **`ANTHROPIC_API_KEY`**: Your Anthropic API key (required for Anthropic)
- **`ANTHROPIC_MODEL`**: Model to use (overrides code default)

### Azure OpenAI Configuration

- **`AZURE_OPENAI_API_KEY`**: Your Azure OpenAI API key (required for Azure)
- **`AZURE_OPENAI_ENDPOINT`**: Your Azure OpenAI endpoint URL (required for Azure)
- **`AZURE_OPENAI_MODEL`**: Model deployment name to use (overrides code default)

### Grader Configuration

The grader LLM is used to evaluate the quality of responses from the main LLM. By default, it uses OpenAI with `DefaultGraderModel` from `evals/evals.go`, which is pinned separately from the model under test. `TestLLMRubricEval` in `evals/graders_test.go` checks the grader against outputs with known verdicts; run it after changing the grader prompt or model. You can configure a different provider or model for grading:

- **`GRADER_LLM_PROVIDER`**: Provider to use for grading (e.g., `openai`, `anthropic`, `azure`)
  - Default: `openai`
  - Uses the same API key environment variables as the main LLM (e.g., `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`)
- **`GRADER_LLM_MODEL`**: Model to use for grading
  - Defaults to `DefaultGraderModel` for OpenAI, and to each provider's code default otherwise

### Examples

```bash
# Run with only OpenAI
LLM_PROVIDER=openai OPENAI_API_KEY=sk-... evalviewer run ./conversations

# Run with only Anthropic
LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-... evalviewer run ./conversations

# Run with OpenAI and Azure (skip Anthropic)
LLM_PROVIDER=openai,azure evalviewer run ./conversations

# Run with all providers (default)
evalviewer run ./conversations

# Use a specific model for OpenAI
OPENAI_MODEL=gpt-5.6-sol evalviewer run ./conversations

# Use Anthropic for the main LLM and OpenAI for grading (default grader)
LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-... OPENAI_API_KEY=sk-... evalviewer run ./conversations

# Use OpenAI for main LLM and Anthropic for grading
LLM_PROVIDER=openai GRADER_LLM_PROVIDER=anthropic OPENAI_API_KEY=sk-... ANTHROPIC_API_KEY=sk-ant-... evalviewer run ./conversations

# Use a specific model for grading
GRADER_LLM_MODEL=gpt-5.6-sol evalviewer run ./conversations
```

If a provider's API key is not set, an error will be thrown.
