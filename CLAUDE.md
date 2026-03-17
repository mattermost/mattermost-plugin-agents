# CLAUDE.md

## Build/Lint/Test Commands
- Build & Deploy plugin: `make deploy`
- Lint code and fix some errors, will edit files if fixes needed: `make check-style-fix`
- Run all tests: `make test`
- Run specific Go test: `go test -v ./server/path/to/package -run TestName`
- Run e2e tests: `make e2e`
- Run specific e2e test file: `cd e2e && npx playwright test filename.spec.ts --reporter=list`
- Run prompt evaluations (CI mode, non-interactive): `make evals-ci`
- Run evals with specific provider: `LLM_PROVIDER=openai make evals-ci` (options: openai, anthropic, azure, openaicompatible, all)
- Run evals with specific model: `ANTHROPIC_MODEL=claude-3-opus-20240229 make evals-ci`
- Run evals with multiple providers: `LLM_PROVIDER=openai,anthropic make evals-ci`
- Run evals with OpenAI compatible API (e.g., local LLMs): `LLM_PROVIDER=openaicompatible OPENAI_COMPATIBLE_API_URL=http://localhost:8080/v1 OPENAI_COMPATIBLE_MODEL=llama-3 make evals-ci`
- Run streaming benchmarks: `go test -bench=. -benchmem ./llm/... ./streaming/...`
- Validate e2e CI shard coverage: `cd e2e && node scripts/ci-test-groups.mjs validate`
- List files assigned to a specific e2e CI shard/group: `cd e2e && node scripts/ci-test-groups.mjs list <group-name>`

## Code Style Guidelines
- Go: Follow Go standard formatting conventions according to goimports
- TypeScript/React: Use 4-space indentation, PascalCase for components, strict typing, always use styled-components, never use style properties
- Error handling: Check all errors explicitly in production code
- File naming: Use snake_case for file names
- Documentation: Include license header in all files
- Use descriptive variable and function names
- Use small, focused functions
- Write go unit tests whenever possible
- Never use mocking or introduce new testing libraries
- Document all public APIs
- Always add i18n for new text
- Write go unit tests as table driven tests whenever possible

## Testing Principles
Write tests that verify behavior which could actually break due to bugs in our code. Before writing a test, ask: "If this test fails, does it indicate a real bug?"

**Don't test:**
- Simple getters/setters that just return or assign a field
- Struct field assignment (creating a struct and checking fields equal what you set)
- Constants equal their values (`assert.Equal(t, "running", JobStatusRunning)`)
- Go standard library behavior (e.g., `strings.Builder`, `map` access)
- Implementation details like validation order or which error appears first

**Avoid:**
- Duplicating production code logic in tests instead of calling the actual function
- Conditional test assertions that accept multiple outcomes (`if x { assert A } else { assert B }`)
- Tests where the only way they can fail is if the Go compiler is broken

**Do test:**
- Functions with actual logic, branching, or calculations
- Error conditions and edge cases in real code paths
- Integration between components
- Behavior that depends on state or external inputs

## E2E CI Shard Maintenance
- The agent/plugin e2e CI sharding is defined in `e2e/scripts/ci-test-groups.mjs`.
- When adding a new e2e spec file that should run on CI, update the appropriate group in that file in the same change.
- Keep non-real-api tests in one of the `e2e-shard-*` groups.
- Keep real-api tests in the dedicated real-api groups (`llmbot-real-*`, `tool-calling-real`, `channel-analysis-real`).
- Prefer balancing new files by expected runtime, not alphabetically. Heavier files should go into lighter shards.
- After changing shard assignments, always run:
  - `cd e2e && node scripts/ci-test-groups.mjs validate`
- If you are unsure where a new spec belongs:
  - put mock/non-real-api tests into the lightest `e2e-shard-*` group
  - put provider-backed tests into the matching real-api group
  - keep provider splitting driven by `E2E_PROVIDER` rather than duplicating files across groups
