<div align="center">

# Mattermost Agents Plugin [![Download Latest Master Build](https://img.shields.io/badge/Download-Latest%20Master%20Build-blue)](https://github.com/mattermost/mattermost-plugin-ai/releases/tag/latest-master)

The Mattermost Agents Plugin integrates AI capabilities directly into your [Mattermost](https://github.com/mattermost/mattermost) workspace. **Run any local LLM** on your infrastructure or connect to cloud providers - you control your data and deployment.

</div>

![The Mattermost Agents AI Plugin is an extension for mattermost that provides functionality for self-hosted and vendor-hosted LLMs](img/mattermost-ai-llm-access.webp)

## Key Features

- **Multiple AI Assistants**: Configure different agents with specialized personalities and capabilities
- **Role-Based Bots**: Specialized PM and Dev agents with intent detection and tool access
- **Multi-Source Data Integration**: Access 10+ external data sources (GitHub, Jira, Confluence, Discourse, etc.)
- **Grounded Responses**: Citation-based and thread-based validation ensures factual accuracy
- **Thread & Channel Summarization**: Get concise summaries of long discussions with a single click
- **Action Item Extraction**: Automatically identify and extract action items from threads
- **Meeting Transcription**: Transcribe and summarize meeting recordings
- **Semantic Search**: Find relevant content across your Mattermost instance using natural language
- **Semantic Caching**: Vector-based caching with pgvector for improved performance
- **Smart Reactions**: Let AI suggest contextually appropriate emoji reactions
- **Direct Conversations**: Chat directly with AI assistants in dedicated channels
- **Flexible LLM Support**: Use local models (Ollama, vLLM, etc.), cloud providers (OpenAI, Anthropic, Azure), or any OpenAI-compatible API
- **Comprehensive Evaluation**: Statistical baseline comparison and model testing frameworks

## Documentation

Comprehensive documentation is available in the `/docs` directory:

- [User Guide](docs/user_guide.md): Learn how to interact with AI features
- [Admin Guide](docs/admin_guide.md): Detailed installation and configuration instructions
- [Provider Setup](docs/providers.md): Configuration for supported LLM providers
- [Feature Documentation](docs/features/): Detailed guides for individual features

## Installation

1. Download the latest release from the [releases page](https://github.com/mattermost/mattermost-plugin-ai/releases). You can also download the **experimental** [latest master](https://github.com/mattermost/mattermost-plugin-ai/releases/tag/latest-master)
2. Upload and enable the plugin through the Mattermost System Console
3. Configure your desired LLM provider settings

### System Requirements

- Mattermost Server v10.0+ (minimum supported: v6.2.1)
- PostgreSQL database with pgvector extension for:
  - Semantic search capabilities
  - Semantic caching (vector-based similarity matching)
  - Thread grounding validation with embeddings
- Network access to your chosen LLM provider
- Optional: External data source access (GitHub, Jira, Confluence, Discourse) for role-based bots

## Quick Start

After installation, complete these steps to get started:

1. Navigate to **System Console > Plugins > Agents**
2. Create an agent and configure it with your LLM provider credentials
3. Set permissions for who can access the agent
4. Open the Agents panel from any channel using the AI icon in the right sidebar
5. Start interacting with your AI assistant

For detailed configuration instructions, see the [Admin Guide](docs/admin_guide.md).

## Integration

### Bridge Client

The plugin provides a Go client library for other Mattermost plugins and the Mattermost server to interact with the AI plugin's LLM Bridge API. This allows you to easily add AI capabilities to your own plugins or server-side features.

See the [Bridge Client README](public/bridgeclient/README.md) for installation and usage instructions.

## Development

### Prerequisites

- Go 1.24+
- Node.js 20.11+
- Access to an LLM provider (OpenAI, Anthropic, etc.)

### Local Setup

1. Setup your Mattermost development environment by following the [Mattermost developer setup guide](https://developers.mattermost.com/contribute/server/developer-setup/). If you have a remote mattermost server you want to develop to you can skip this step. 

2. Setup your Mattermost plugin development environment by following the [Plugin Developer setup guide](https://developers.mattermost.com/integrate/plugins/developer-setup/).

3. Clone the repository:
```bash
git clone https://github.com/mattermost/mattermost-plugin-ai.git
cd mattermost-plugin-ai
```

4. **Optional**. If you are developing to a remote server, setup environment variables to deploy:
```bash
MM_SERVICESETTINGS_SITEURL=http://localhost:8065
MM_ADMIN_USERNAME=<YOUR_USERNAME>
MM_ADMIN_PASSWORD=<YOUR_PASSWORD>
```

5. Run deploy to build the plugin
```bash
make deploy
```

### Other make commands

- Run `make help` for a list of all make commands
- Run `make check-style` to verify code style
- Run `make test` to run the test suite
- Run `make e2e` to run the e2e tests
- Run `make evals` to run prompt evaluations interactively (with TUI)
- Run `make evals-ci` to run prompt evaluations in CI mode (non-interactive)

### Benchmark Tests

The streaming code has benchmark tests to measure performance and detect regressions:

```bash
# Run all streaming benchmarks
go test -bench=. -benchmem ./llm/... ./streaming/...

# Run specific benchmark
go test -bench=BenchmarkStreamToPost -benchmem ./streaming/...

# Run with CPU profiling
go test -bench=BenchmarkReadAll -cpuprofile=cpu.prof ./llm/...
```

Benchmarks cover:
- `ReadAll()` stream consumption with varying response sizes
- `TokenUsageLoggingWrapper` interception overhead
- `StreamToPost()` full processing pipeline with WebSocket events

### Multi-Provider Evaluation Support

The evaluation system supports testing with multiple LLM providers: OpenAI, Anthropic, and Azure OpenAI. By default, evaluations run against all providers, but you can target specific ones:

```bash
# Run with all providers (default)
make evals

# Run with only Anthropic
LLM_PROVIDER=anthropic make evals

# Run with OpenAI and Azure
LLM_PROVIDER=openai,azure make evals

# Use a specific model
ANTHROPIC_MODEL=claude-3-opus-20240229 make evals
```

See `cmd/evalviewer/README.md` for complete documentation on environment variables and configuration options.

## Specialized Role-Based AI Bots

This plugin includes role-specific AI assistants with specialized capabilities, comprehensive evaluation frameworks, and grounded data retrieval:

### PM Agent - Product Management Assistant

A specialized AI assistant for product management tasks with access to product data sources, market intelligence, and strategic analysis tools.

**Key Capabilities:**
- **Intent Detection**: Automatically recognizes 9 PM intent types (task creation, task updates, status reporting, strategic alignment, feature gap analysis, market research, meeting facilitation, standup summary, action items)
- **Specialized Tools**:
  - `CompileMarketResearch`: Competitive analysis, market trends, customer feedback aggregation
  - `AnalyzeFeatureGaps`: Identifies competitive gaps, user requests, technical debt
  - `AnalyzeStrategicAlignment`: Vision alignment, RICE framework application, stakeholder balancing
- **Multi-Source Data Integration**:
  - External docs: Mattermost docs, handbook, blog, forum, newsroom, GitHub repos
  - Project management: Jira, Confluence, ProductBoard, Zendesk, UserVoice
  - Internal: Team conversations with semantic search and time-range filtering
- **Context-Aware Prompts**: Dynamic prompt selection based on detected intent
- **Semantic Caching**: Vector-based similarity caching for improved performance
- **Mock Mode**: Testing support with fallback data for offline development

**Evaluation Framework:**
- **Scenarios**: 54 total (24 junior, 30 senior) across generic and mattermost-specific variants
  - Junior (24): 12 generic + 12 mattermost-specific (sprint planning, bug triage, dependency management, stakeholder updates)
  - Senior (30): 15 generic + 15 mattermost-specific (vision alignment, feature prioritization, technical debt strategy, market analysis)
- **Rubrics**: Data provenance, quantitative reasoning, framework application, strategic thinking (6-10 rubrics per level)
  - Junior generic: 6 rubrics | Junior mattermost: 8 rubrics
  - Senior generic: 8 rubrics | Senior mattermost: 10 rubrics
- **Evaluation Method**: Threshold-based (STRICT: all pass, MODERATE: majority, LAX: minimum 2)
- **Model Comparison**: Statistical baseline vs enhanced bot testing with confidence intervals

For detailed documentation, see [roles/pm/README.md](roles/pm/README.md).

### Dev Agent - Developer Assistant

A specialized AI assistant for software development with access to technical documentation, codebase search, and architecture knowledge.

**Key Capabilities:**
- **Intent Detection**: Automatically recognizes debugging, code explanation, architecture, API usage, and PR summary requests
- **Specialized Tools**:
  - `ExplainCodePattern`: Implementation patterns with real code examples from Mattermost repos
  - `DebugIssue`: Error troubleshooting with solutions from community forums and issues
  - `FindArchitecture`: ADRs, system design patterns, architectural decisions
  - `GetAPIExamples`: Real-world API and plugin hook usage from actual code
  - `SummarizePRs`: Recent changes, releases, and code evolution tracking
- **Multi-Source Data Integration**:
  - GitHub: Code search, issues, PRs, releases across Mattermost repositories
  - Documentation: Official dev docs, API references, integration guides
  - Confluence: Internal architecture docs, ADRs, design decisions
  - Community: Mattermost forum (Discourse), plugin marketplace
  - Jira: Issue tracking and feature documentation
- **Context-Aware Prompts**: Specialized system prompts per intent (debugging, architecture, API usage)
- **Citation Requirements**: File paths, line numbers, ADR references, working code examples
- **Semantic Caching**: Performance optimization for repeated queries

**Evaluation Framework:**
- **Scenarios**: 22 total (12 junior, 10 senior) - all mattermost-specific
  - Junior (12): Plugin development, debugging, API usage, manifest validation
  - Senior (10): HA architecture, scalability, security, performance optimization
- **Rubrics**: MM API accuracy, working code examples with file paths/line numbers, architecture understanding, security practices, ADR references, production readiness
  - Junior: 8 rubrics | Senior: 10 rubrics
- **Evaluation Method**: Threshold-based (STRICT: all pass, MODERATE: majority, LAX: minimum 2)

**Supported Repositories:**
- `mattermost/mattermost`, `mattermost-webapp`, `mattermost-mobile`
- `desktop`, `mattermost-plugin-ai`, `mattermost-plugin-starter-template`
- `mattermost-api-reference`

For detailed documentation, see [roles/dev/README.md](roles/dev/README.md).

## Data Sources & Grounding System

The plugin includes a comprehensive datasources infrastructure that enables role-based bots to fetch, validate, and cite information from multiple external sources.

### Datasources Infrastructure

**Protocol-Based Architecture:**
- Unified interface for 10+ data source protocols
- Standardized document format with metadata extraction
- Rate limiting and circuit breaker patterns for reliability
- Boolean query support with automatic simplification
- Quality scoring and content validation

**Supported Protocols:**

1. **GitHub** (`github_protocol_core.go`):
   - Issues, PRs, releases, and code search
   - Boolean query syntax (AND, OR, NOT, parentheses)
   - Repository relevance scoring
   - Rate limit handling with automatic retry

2. **Jira** (`jira_protocol.go`):
   - JQL search with custom field extraction
   - Issue type filtering and metadata parsing
   - Status, priority, and label extraction

3. **Confluence** (`confluence_protocol.go`):
   - CQL search across spaces
   - Page content extraction with HTML processing
   - ADR and requirements documentation retrieval

4. **HTTP/Web** (`http_protocol.go`):
   - Structured HTML content extraction
   - Semantic section detection
   - Article link discovery from listing pages
   - Circuit breaker for domain failure tracking

5. **Discourse** (`discourse_protocol.go`):
   - Community forum topic and post search
   - Engagement metrics (likes, views, posts)
   - Tag-based filtering

6. **Mattermost** (`mattermost_protocol.go`):
   - Cross-server post search
   - Channel browsing by section
   - Dynamic channel mapping

7. **File-Based Sources** (`file_protocol.go`, `file_protocol_*.go`):
   - ProductBoard features (`file_protocol_productboard.go`): JSON parsing, vote tracking, status filtering
   - Zendesk tickets (`file_protocol_zendesk.go`): Critical issue identification, ticket parsing
   - UserVoice exports (`uservoice_protocol.go`): Feature request aggregation (source disabled - using file fallback)
   - Hub data (`file_protocol_hub.go`): Customer feedback, lost deals analysis

**Advanced Features:**
- **Semantic Caching**: Vector-based similarity caching with pgvector (supports OpenAI and OpenAI-compatible providers like Ollama, vLLM). Models: `text-embedding-3-small` (1536 dims), `nomic-embed-text` (768 dims)
- **Simple TTL Cache**: In-memory cache with configurable TTL (default: 24 hours), background cleanup, thread-safe operations
- **Boolean Query Engine**: Full expression tree parsing with operator precedence (AND, OR, NOT, parentheses, quoted phrases). Max depth: 20 levels with keyword extraction fallback
- **Rate Limiting**: Token bucket algorithm per protocol (configurable req/min and burst size). Defaults: HTTP (30 req/min, burst 5), GitHub (60 req/min, burst 10), Confluence/Jira (15 req/min, burst 3)
- **Circuit Breaker**: Domain-level failure tracking with 5-minute sliding window (max 50 failures before opening circuit)
- **Content Quality Scoring**: Multi-dimensional scoring (0-100 scale) based on content quality, semantic relevance, and source authority
- **Topic Analysis**: Feature taxonomy with synonym expansion, keyword weighting
- **Metadata Extraction**: Customer segments, technical categories, competitors, priority levels

**Configuration:**
```bash
# Environment Variables for Data Source Authentication
export MM_AI_GITHUB_TOKEN="ghp_..."
export MM_AI_CONFLUENCE_URL="https://yourcompany.atlassian.net/wiki"
export MM_AI_CONFLUENCE_USERNAME="user@example.com"
export MM_AI_CONFLUENCE_API_TOKEN="ATATT..."
export MM_AI_JIRA_TOKEN="your-jira-token"
export MM_AI_DISCOURSE_URL="https://community.mattermost.com"

# Source-specific tokens (pattern: MM_AI_<SOURCE_NAME>_TOKEN)
export MM_AI_<SOURCE_NAME>_TOKEN="your-token"
```

### Grounding Validation System

**Purpose:** Citation-based validation framework for PM/Dev bot responses to detect fabricated information and ensure factual accuracy.

**Validation Process:**
- Automatically extracts citations from LLM responses: Jira tickets (`MM-12345`), GitHub issues/PRs (`org/repo#123`, `#123`), URLs, ProductBoard IDs, Zendesk tickets
- Three-phase validation:
  1. **Reference Index Matching**: Exact-match citations against tool results returned to bot
  2. **API Verification**: External API calls (GitHub, Jira) for citations not in tool results
  3. **URL Accessibility**: HTTP status checking for web links
- **Metadata Claim Validation**: Verifies accuracy of priority, segments, categories mentioned in response
- **Grounding Metrics**: Citation density (citations per 100 words), validation rate (% valid), fabrication rate (% fabricated), claim accuracy rate

**Validation Status Types:**
- `ValidationGrounded`: Citation found in tool results provided to bot
- `ValidationUngroundedValid`: Not in tool results but verified via external API as real
- `ValidationUngroundedBroken`: URL inaccessible (404, 500 errors)
- `ValidationFabricated`: Citation does not exist in any source (hallucinated)
- `ValidationAPIError`: API verification failed (network/auth issues)
- `ValidationNotChecked`: Validation not performed (no credentials)

**Thresholds:**
```go
// Balanced (default)
MinCitationRate:   0.70
MinMetadataFields: 2
CitationWeight:    0.7
MetadataWeight:    0.3

// Strict (production)
MinCitationRate:   0.85
MinMetadataFields: 3
CitationWeight:    0.8
MetadataWeight:    0.2

// Lax (development)
MinCitationRate:   0.50
MinMetadataFields: 1
CitationWeight:    0.6
MetadataWeight:    0.4
```

**Usage in Tests:**
```go
result := grounding.EvaluateGroundingWithLogging(
    evalT,
    response,
    toolResults,
    metadata,
    apiClients,
    grounding.StrictThresholds(),
    "PM Bot",
)
// result.Pass, result.GroundedCitations, result.FabricatedCitations
```

**Note:** The codebase also includes thread-based validation (`grounding/thread/`) for validating conversation summaries and meeting notes against actual thread content using semantic similarity and participant verification. This is a separate feature not currently used in PM/Dev bot evaluations.

For implementation details, see `/grounding/` and `/datasources/` directories.

### Testing Data Source Integrations

**Integration Tests:**
```bash
# GitHub (code search, PRs, metadata)
MM_AI_GITHUB_TOKEN=xxx go test -v -tags=integration ./datasources -run TestGitHub

# Jira (authentication and search)
MM_AI_JIRA_TOKEN=xxx go test -v -tags=integration ./datasources -run TestJira

# Confluence (CQL search, metadata)
MM_AI_CONFLUENCE_TOKEN=xxx go test -v -tags=integration ./datasources -run TestConfluence

# Discourse (forum search, metadata)
go test -v -tags=integration ./datasources -run TestDiscourse

# HTTP protocol (circuit breaker, content extraction)
go test -v -tags=integration ./datasources -run TestHTTP

# Semantic cache integration (vector embeddings)
go test -v ./semanticcache -run TestIntegration

# Cross-protocol integration test
go test -v -tags=integration ./datasources -run TestIntegration
```

**Unit Tests:**
```bash
# All datasource unit tests
go test -v ./datasources

# Grounding validation
go test -v ./grounding
go test -v ./grounding/thread
go test -v ./grounding/semantic

# Boolean query engine
go test -v ./datasources -run TestBooleanQuery

# Rate limiting
go test -v ./datasources -run TestRateLimiter

# Cache implementations
go test -v ./datasources -run TestCache
```

## Evaluation System

The plugin includes comprehensive evaluation capabilities for testing and comparing AI assistant performance across all specialized bots:

### Baseline Evaluation Framework

Compare enhanced bots (with custom prompts and tool access) against raw LLM baselines to quantify the value of plugin enhancements.

**Key Capabilities:**
- Statistical comparison with confidence intervals and significance testing
- Two baseline modes: vanilla (minimal prompt) or role-prompt (same custom prompts, no tool access)
- Isolate improvements from prompts vs tool access
- Flexible configuration with command-line flags and environment variables

**Quick Start:**
```bash
# Run PM bot evaluation (all scenarios)
GOEVALS=1 go test -v ./roles/pm -run TestPMBotVsBaselineComparison

# Compare with fair baseline (isolate tool value)
GOEVALS=1 go test -v ./roles/pm -run TestPMBotVsBaselineComparison -role-prompt-baseline
```

### PM Agent Evaluations

Specialized evaluation tests for product management capabilities:

**Available Tests:**
- **PM Baseline Comparison**: Tests all scenarios (junior + senior, generic + mattermost-specific) with statistical analysis
- **Model Comparison**: Compare different LLMs in baseline or enhanced modes across all scenarios
- Use `-level=junior|senior` to select junior or senior scenarios, `-scenarios=CORE|BREADTH|ALL` to filter subsets (PM only)

**Test Configuration:**
- `GOEVALS=N`: Control test repetitions for statistical confidence
- `TEST_MODEL`: Specify model (supports comma-separated lists for comparisons)
- `-level=junior|senior`: Select skill level (junior or senior scenarios)
- `-scenarios=CORE|BREADTH|ALL`: Choose scenario subset (PM only, default: ALL)
- `-mm-centric`: Use Mattermost-specific scenarios requiring data source access
- `-threshold=STRICT|MODERATE|LAX`: Set rubric pass criteria (default: MODERATE)
- `-role-prompt-baseline`: Fair comparison mode (same custom prompts, no tool access - isolates tool value)
- `-comparison-mode=baseline|enhanced|both`: Model comparison mode (default: enhanced)
- `-temperature=N`: LLM temperature 0.0-1.0 (default: model default)
- `-timeout=Ns`: Request timeout, e.g., 60s, 2m (default: 60s)
- `-debug`: Enable detailed debug logging
- `-warn`: Enable high-level progress logging
- `-grader-model=MODEL`: Override model used for rubric grading
- `-save-prompts`: Save prompts/outputs to files for analysis
- `-grounding`: Enable grounding validation (citation analysis)
- `-disable-thinking`: Disable extended thinking for Anthropic models (reduces token usage, avoids tool use conflicts)

**Example Usage:**
```bash
# PM baseline comparison (all scenarios, both junior and senior)
GOEVALS=1 go test -v ./roles/pm -run TestPMBotVsBaselineComparison -threshold=MODERATE

# Junior PM only (24 scenarios with 6-8 rubrics)
GOEVALS=1 go test -v ./roles/pm -run TestPMBotVsBaselineComparison -level=junior -threshold=MODERATE

# Senior PM only (30 scenarios with 8-10 rubrics)
GOEVALS=1 go test -v ./roles/pm -run TestPMBotVsBaselineComparison -level=senior -threshold=STRICT

# Mattermost-specific scenarios requiring data sources
GOEVALS=1 go test -v ./roles/pm -run TestPMBotVsBaselineComparison -mm-centric

# Fair baseline comparison (same prompts, no tools)
GOEVALS=1 go test -v ./roles/pm -run TestPMBotVsBaselineComparison -role-prompt-baseline

# Compare models in enhanced mode
GOEVALS=2 TEST_MODEL="gpt-4o,claude-3-5-sonnet" go test -v ./roles/pm -run TestPMBotModelComparison -comparison-mode=enhanced

# High-confidence testing with multiple repetitions
GOEVALS=10 go test -v ./roles/pm -run TestPMBotVsBaselineComparison

# Comprehensive PM evaluation with all features enabled
GOEVALS=1 TEST_MODEL=claude-sonnet-4 go test -v ./roles/pm -run TestPMBotVsBaselineComparison \
  -temperature=0 \
  -timeout=180m \
  -level=senior \
  -scenarios=CORE \
  -grader-model=claude-sonnet-4 \
  -mm-centric \
  -save-prompts \
  -save-output-dir=../../eval_output/pm_results \
  -role-prompt-baseline \
  -grounding \
  -debug \
  -disable-thinking
```

### Dev Agent Evaluations

Specialized evaluation tests for developer assistant capabilities:

**Available Tests:**
- **Dev Baseline Comparison**: Tests all scenarios (junior + senior mattermost-specific) with statistical analysis
- **Model Comparison**: Compare different LLMs in baseline or enhanced modes
- Use `-level=junior|senior` to select junior or senior scenarios

**Example Usage:**
```bash
# Basic Dev bot comparison
GOEVALS=10 go test -v ./roles/dev -run TestJuniorDevBotVsBaselineComparison

# Senior Dev bot evaluation with strict grading
GOEVALS=10 go test -v ./roles/dev -run TestSeniorDevBotVsBaselineComparison -threshold=STRICT

# Fair baseline comparison (same prompts, no tools)
GOEVALS=10 go test -v ./roles/dev -run TestDevBotVsBaselineComparison -role-prompt-baseline

# Dev bot model comparison
GOEVALS=5 TEST_MODEL="gpt-4o,claude-3-5-sonnet" go test -v ./roles/dev -run TestDevBotModelComparison -comparison-mode=enhanced

# Comprehensive Dev evaluation with all features enabled
GOEVALS=1 TEST_MODEL=claude-sonnet-4 go test -v ./roles/dev -run TestDevBotVsBaselineComparison \
  -temperature=0 \
  -timeout=180m \
  -level=senior \
  -grader-model=claude-sonnet-4 \
  -mm-centric \
  -save-prompts \
  -save-output-dir=../../eval_output/dev_results \
  -role-prompt-baseline \
  -grounding \
  -debug \
  -disable-thinking
```

For comprehensive documentation on evaluation tests, statistical interpretation, scenario design, and creating custom evaluations, see the detailed guides in `roles/pm/`, `roles/dev/`, and `evals/baseline/` directories.


## License

This repository is licensed under [Apache-2](./LICENSE), except for the [server/enterprise](server/enterprise) directory which is licensed under the [Mattermost Source Available License](LICENSE.enterprise). See [Mattermost Source Available License](https://docs.mattermost.com/overview/faq.html#mattermost-source-available-license) to learn more.
