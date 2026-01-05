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

## Environment Variables

The evalviewer and evaluation tests support multiple LLM providers. Configure which providers to use and their settings with these environment variables:

### Provider Selection

- **`LLM_PROVIDER`**: Choose which provider(s) to run evaluations with
  - Values: `openai`, `anthropic`, `azure`, `all`, or comma-separated (e.g., `openai,azure`)
  - Default: `all` (runs all providers)

### OpenAI Configuration

- **`OPENAI_API_KEY`**: Your OpenAI API key (required for OpenAI)
- **`OPENAI_MODEL`**: Model to use (default: `gpt-4o`)

### Anthropic Configuration

- **`ANTHROPIC_API_KEY`**: Your Anthropic API key (required for Anthropic)
- **`ANTHROPIC_MODEL`**: Model to use (default: `claude-sonnet-4-20250514`)

### Azure OpenAI Configuration

- **`AZURE_OPENAI_API_KEY`**: Your Azure OpenAI API key (required for Azure)
- **`AZURE_OPENAI_ENDPOINT`**: Your Azure OpenAI endpoint URL (required for Azure)
- **`AZURE_OPENAI_MODEL`**: Model deployment name to use (default: `gpt-4o`)

### Grader Configuration

The grader LLM is used to evaluate the quality of responses from the main LLM. By default, it uses OpenAI with the `gpt-5` model. You can configure a different provider or model for grading:

- **`GRADER_LLM_PROVIDER`**: Provider to use for grading (e.g., `openai`, `anthropic`, `azure`)
  - Default: `openai`
  - Uses the same API key environment variables as the main LLM (e.g., `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`)
- **`GRADER_LLM_MODEL`**: Model to use for grading
  - Default: `gpt-5` (for OpenAI provider)
  - For other providers, uses their default model unless specified

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
OPENAI_MODEL=gpt-4-turbo evalviewer run ./conversations

# Use Anthropic for the main LLM and OpenAI gpt-5 for grading (default grader)
LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-... OPENAI_API_KEY=sk-... evalviewer run ./conversations

# Use OpenAI gpt-4o for main LLM and Anthropic for grading
LLM_PROVIDER=openai GRADER_LLM_PROVIDER=anthropic OPENAI_API_KEY=sk-... ANTHROPIC_API_KEY=sk-ant-... evalviewer run ./conversations

# Use a specific model for grading
GRADER_LLM_MODEL=gpt-4o evalviewer run ./conversations
```

If a provider's API key is not set, an error will be thrown.
