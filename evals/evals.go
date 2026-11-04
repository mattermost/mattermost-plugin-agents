// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
)

// Default models for each provider. Update these when bumping model versions.
const (
	DefaultOpenAIModel    = "gpt-5.6-terra"
	DefaultAnthropicModel = "claude-sonnet-5-5"
	// DefaultAzureModel is the deployment name in the CI Azure OpenAI resource.
	DefaultAzureModel   = "gpt-6.1-sol"
	DefaultMistralModel = "mistral-large-latest"
	DefaultBedrockModel = "global.anthropic.claude-sonnet-5-5"
	DefaultCohereModel  = "command-a-plus-05-2026"

	// DefaultGraderModel is pinned separately from DefaultOpenAIModel so that
	// bumping the model under test does not silently change the judge.
	DefaultGraderModel = "gpt-5.6-terra"
)

type EvalT struct {
	*testing.T
	*Eval

	// name is the provider-level test name results are recorded under, so
	// every trial of an eval shares one name.
	name string
	// recorded counts graded results in the current run; failures holds its
	// assertion messages other than rubric verdicts.
	recorded int
	failures []string
}

// Errorf captures assertion failures (testify reports through it) so they are
// recorded with the run's results.
func (e *EvalT) Errorf(format string, args ...any) {
	e.Helper()
	e.failures = append(e.failures, fmt.Sprintf(format, args...))
	e.T.Errorf(format, args...)
}

type Eval struct {
	LLM       llm.LanguageModel
	GraderLLM llm.LanguageModel
	Prompts   *llm.Prompts

	Provider    string
	Model       string
	GraderModel string

	agent     roleModel
	grader    roleModel
	runNumber int
}

// roleModel is the model behind one role, with the names the preflight reports.
type roleModel struct {
	role     string
	provider string
	model    string
	llm      llm.LanguageModel
}

// simpleProviders describes providers that need only an API key and a model.
// Providers with extra requirements (azure, openaicompatible, bedrock) are
// handled as explicit branches in serviceConfigFor.
var simpleProviders = map[string]struct {
	serviceType  string
	keyEnv       string
	modelEnv     string
	defaultModel string
}{
	"openai":    {llm.ServiceTypeOpenAI, "OPENAI_API_KEY", "OPENAI_MODEL", DefaultOpenAIModel},
	"anthropic": {llm.ServiceTypeAnthropic, "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", DefaultAnthropicModel},
	"mistral":   {llm.ServiceTypeMistral, "MISTRAL_API_KEY", "MISTRAL_MODEL", DefaultMistralModel},
	"cohere":    {llm.ServiceTypeCohere, "COHERE_API_KEY", "COHERE_MODEL", DefaultCohereModel},
}

// agentUnderTest mirrors the defaults of an agent created in the admin UI, so
// evals exercise the same provider settings customers get out of the box.
var agentUnderTest = llm.BotConfig{ReasoningEnabled: true}

// graderAgent keeps grading cheap and stable; reasoning adds latency and
// variance without improving single-criterion rubric checks.
var graderAgent = llm.BotConfig{ReasoningEnabled: false}

// resolveModel picks the model to use: the explicit override, then the
// provider's model environment variable, then the provider default.
func resolveModel(override, modelEnv, defaultModel string) string {
	if override != "" {
		return override
	}
	if model := os.Getenv(modelEnv); model != "" {
		return model
	}
	return defaultModel
}

// serviceConfigFor builds the service configuration an admin would create for
// the provider, reading credentials from environment variables.
func serviceConfigFor(providerName string, modelOverride string) (llm.ServiceConfig, error) {
	name := strings.ToLower(providerName)

	if p, ok := simpleProviders[name]; ok {
		apiKey := os.Getenv(p.keyEnv)
		if apiKey == "" {
			return llm.ServiceConfig{}, fmt.Errorf("%s environment variable is not set", p.keyEnv)
		}
		return llm.ServiceConfig{
			ID:           name,
			Type:         p.serviceType,
			APIKey:       apiKey,
			DefaultModel: resolveModel(modelOverride, p.modelEnv, p.defaultModel),
		}, nil
	}

	switch name {
	case "azure":
		apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
		if apiKey == "" {
			return llm.ServiceConfig{}, errors.New("AZURE_OPENAI_API_KEY environment variable is not set")
		}
		apiURL := os.Getenv("AZURE_OPENAI_ENDPOINT")
		if apiURL == "" {
			return llm.ServiceConfig{}, errors.New("AZURE_OPENAI_ENDPOINT environment variable is not set")
		}
		return llm.ServiceConfig{
			ID:           name,
			Type:         llm.ServiceTypeAzure,
			APIKey:       apiKey,
			APIURL:       apiURL,
			DefaultModel: resolveModel(modelOverride, "AZURE_OPENAI_MODEL", DefaultAzureModel),
			// New services default to the Responses API in the admin UI.
			UseResponsesAPI: true,
		}, nil

	case "openaicompatible":
		apiURL := os.Getenv("OPENAI_COMPATIBLE_API_URL")
		if apiURL == "" {
			return llm.ServiceConfig{}, errors.New("OPENAI_COMPATIBLE_API_URL environment variable is not set")
		}
		model := resolveModel(modelOverride, "OPENAI_COMPATIBLE_MODEL", "")
		if model == "" {
			return llm.ServiceConfig{}, errors.New("OPENAI_COMPATIBLE_MODEL environment variable is not set")
		}
		return llm.ServiceConfig{
			ID:   name,
			Type: llm.ServiceTypeOpenAICompatible,
			// API key is optional for local LLMs
			APIKey:       os.Getenv("OPENAI_COMPATIBLE_API_KEY"),
			APIURL:       apiURL,
			DefaultModel: model,
		}, nil

	case "bedrock":
		region := os.Getenv("AWS_BEDROCK_REGION")
		if region == "" {
			return llm.ServiceConfig{}, errors.New("AWS_BEDROCK_REGION environment variable is not set")
		}
		return llm.ServiceConfig{
			ID:                 name,
			Type:               llm.ServiceTypeBedrock,
			Region:             region,
			AWSAccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
			AWSSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
			DefaultModel:       resolveModel(modelOverride, "AWS_BEDROCK_MODEL", DefaultBedrockModel),
		}, nil

	default:
		return llm.ServiceConfig{}, fmt.Errorf("unknown provider: %s", providerName)
	}
}

// createProvider builds the provider through the same bifrost service and
// agent construction the plugin uses for configured agents.
func createProvider(providerName string, modelOverride string, agent llm.BotConfig) (roleModel, error) {
	serviceConfig, err := serviceConfigFor(providerName, modelOverride)
	if err != nil {
		return roleModel{}, err
	}

	client, err := bifrost.NewServiceClient(serviceConfig, nil)
	if err != nil {
		return roleModel{}, err
	}

	agentLLM, err := client.ForAgent(agent)
	if err != nil {
		client.Shutdown()
		return roleModel{}, err
	}
	return roleModel{provider: strings.ToLower(providerName), model: serviceConfig.DefaultModel, llm: agentLLM}, nil
}

func NewEval() (*Eval, error) {
	// Default to OpenAI for backward compatibility
	return NewEvalWithProvider("openai")
}

// NewEvalWithProvider creates an Eval instance with a specific provider
func NewEvalWithProvider(providerName string) (*Eval, error) {
	// Setup prompts
	prompts, err := llm.NewPrompts(prompts.PromptsFolder)
	if err != nil {
		return nil, err
	}

	// Create provider (uses environment variables)
	agent, err := createProvider(providerName, "", agentUnderTest)
	if err != nil {
		return nil, err
	}
	agent.role = "model under test"

	// Setup grader LLM (separate from main LLM)
	grader, err := createGraderLLM()
	if err != nil {
		return nil, fmt.Errorf("failed to create grader LLM: %w", err)
	}
	grader.role = "grader"

	return &Eval{
		Prompts:     prompts,
		LLM:         agent.llm,
		GraderLLM:   grader.llm,
		Provider:    agent.provider,
		Model:       agent.model,
		GraderModel: grader.model,
		agent:       agent,
		grader:      grader,
	}, nil
}

// createGraderLLM creates a separate LLM for grading based on environment variables.
// Defaults to OpenAI if not specified.
func createGraderLLM() (roleModel, error) {
	graderProvider := os.Getenv("GRADER_LLM_PROVIDER")
	if graderProvider == "" {
		graderProvider = "openai"
	}

	graderModel := os.Getenv("GRADER_LLM_MODEL")
	if graderModel == "" && graderProvider == "openai" {
		graderModel = DefaultGraderModel
	}

	return createProvider(graderProvider, graderModel, graderAgent)
}

func NumEvalsOrSkip(t *testing.T) int {
	t.Helper()
	numEvals, err := strconv.Atoi(os.Getenv("GOEVALS"))
	if err != nil || numEvals < 1 {
		t.Skip("Skipping evals. Use GOEVALS=1 flag to run.")
	}

	return numEvals
}

func Run(t *testing.T, name string, f func(e *EvalT)) {
	numEvals := NumEvalsOrSkip(t)

	// Get list of providers to test
	providers := getProvidersToTest()

	// Run evaluations for each provider
	for _, providerName := range providers {
		// Try to create eval for this provider
		eval, err := NewEvalWithProvider(providerName)
		if err != nil {
			t.Logf("Skipping %s provider: %v", providerName, err)
			continue
		}

		e := &EvalT{T: t, Eval: eval}

		// Prefix test name with provider
		testName := fmt.Sprintf("[%s] %s", providerName, name)

		t.Run(testName, func(t *testing.T) {
			e.name = t.Name()
			// Each trial is a subtest so a require failure, which ends the
			// running test, does not skip the remaining trials.
			for i := range numEvals {
				t.Run(fmt.Sprintf("run_%d", i), func(t *testing.T) {
					e.T = t
					e.runNumber = i
					runOnce(e, f)
				})
			}
		})
	}
}

// runOnce runs a single trial. The deferred recording also runs when a require
// assertion stops the trial early via runtime.Goexit.
func runOnce(e *EvalT, f func(e *EvalT)) {
	e.recorded = 0
	e.failures = nil
	defer func() {
		if line, ok := failureLine(e.recorded, e.failures, e.Failed()); ok {
			writeLogLine(e, line)
		}
	}()

	for _, role := range []roleModel{e.agent, e.grader} {
		if err := preflight(role); err != nil {
			e.Errorf("%v", err)
			e.FailNow()
		}
	}
	f(e)
}

var (
	preflightMu      sync.Mutex
	preflightResults = map[string]error{}
)

// preflight sends one tiny request per provider and model, once per test
// binary, so a credential or model-access problem fails every eval at once
// with an error that names the model instead of repeating per eval. Transient
// errors (rate limits, server errors) are retried and never cached, so they
// cost at most the evals that hit them.
func preflight(role roleModel) error {
	key := role.provider + "/" + role.model
	preflightMu.Lock()
	defer preflightMu.Unlock()
	if err, done := preflightResults[key]; done {
		return err
	}

	var err error
	for attempt := 0; ; attempt++ {
		err = preflightRequest(role)
		if err == nil || !isTransientProviderError(err) || attempt == len(preflightBackoff) {
			break
		}
		time.Sleep(preflightBackoff[attempt])
	}
	if err != nil {
		err = fmt.Errorf("%s %s/%s is unusable: %w%s", role.role, role.provider, role.model, err, preflightHint(err))
		if isTransientProviderError(err) {
			return err
		}
	}
	preflightResults[key] = err
	return err
}

func preflightRequest(role roleModel) error {
	ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
	defer cancel()
	_, err := role.llm.ChatCompletionNoStream(ctx, llm.CompletionRequest{
		Posts:     []llm.Post{{Role: llm.PostRoleUser, Message: "Reply with the word OK."}},
		Context:   llm.NewContext(),
		Operation: llm.OperationEvalGrading,
	}, llm.WithMaxGeneratedTokens(64), llm.WithReasoningDisabled(), llm.WithToolsDisabled())
	return err
}

const preflightTimeout = 2 * time.Minute

// preflightBackoff is on top of bifrost's own short retries, long enough for
// per-minute rate limits to reset.
var preflightBackoff = []time.Duration{15 * time.Second, 45 * time.Second}

// isTransientProviderError reports rate limits, server errors, and timeouts.
// Bifrost reports rejected credentials as status 502 and exhausted quota as
// status 429, neither of which is transient.
func isTransientProviderError(err error) bool {
	msg := err.Error()
	if strings.Contains(msg, "upstream_credentials_exhausted") || strings.Contains(msg, "insufficient_quota") {
		return false
	}
	for _, marker := range []string{"status=429", "status=500", "status=502", "status=503", "status=504", "rate_limit", "context deadline exceeded"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// preflightHint explains errors whose provider message bifrost discards.
func preflightHint(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "upstream_credentials_exhausted"), strings.Contains(msg, "status=401"), strings.Contains(msg, "status=403"):
		return ". The provider rejected the request as unauthorized: check that the API key is valid and that its project has access to this model"
	case strings.Contains(msg, "DeploymentNotFound"), strings.Contains(msg, "model_not_found"):
		return ". The model or deployment does not exist for these credentials: set the provider's model variable or create it"
	default:
		return ""
	}
}

// getProvidersToTest returns the list of providers to test based on LLM_PROVIDER env var
func getProvidersToTest() []string {
	providerEnv := os.Getenv("LLM_PROVIDER")
	if providerEnv == "" {
		providerEnv = "all"
	}

	providerEnv = strings.ToLower(strings.TrimSpace(providerEnv))

	// Handle "all" case
	if providerEnv == "all" {
		return []string{"openai", "anthropic", "azure", "mistral", "bedrock", "cohere"}
	}

	// Handle comma-separated list (a single provider is a one-element list)
	providers := strings.Split(providerEnv, ",")
	result := make([]string, 0, len(providers))
	for _, p := range providers {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
