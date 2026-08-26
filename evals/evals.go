// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/maximhq/bifrost/core/schemas"
)

// Default models for each provider. Update these when bumping model versions.
const (
	DefaultOpenAIModel    = "gpt-5.2"
	DefaultAnthropicModel = "claude-sonnet-4-6"
	DefaultAzureModel     = "gpt-5.2"
	DefaultMistralModel   = "mistral-large-latest"
	DefaultBedrockModel   = "global.anthropic.claude-sonnet-4-6-v1:0"
)

type EvalT struct {
	*testing.T
	*Eval
}

type Eval struct {
	LLM       llm.LanguageModel
	GraderLLM llm.LanguageModel
	Prompts   *llm.Prompts

	runNumber int
}

// simpleProviders describes providers that need only an API key and a model.
// Providers with extra requirements (azure, openaicompatible, bedrock) are
// handled as explicit branches in createProvider.
var simpleProviders = map[string]struct {
	provider     schemas.ModelProvider
	keyEnv       string
	modelEnv     string
	defaultModel string
	reasoning    bool
}{
	"openai":    {schemas.OpenAI, "OPENAI_API_KEY", "OPENAI_MODEL", DefaultOpenAIModel, false},
	"anthropic": {schemas.Anthropic, "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", DefaultAnthropicModel, true},
	"mistral":   {schemas.Mistral, "MISTRAL_API_KEY", "MISTRAL_MODEL", DefaultMistralModel, false},
	"cohere":    {schemas.Cohere, "COHERE_API_KEY", "COHERE_MODEL", "command-r-plus", false},
}

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

// createProvider creates an LLM provider based on the provider name using Bifrost
// Reads configuration from environment variables with optional model override
func createProvider(providerName string, modelOverride string) (llm.LanguageModel, error) {
	timeout := 20 * time.Second
	name := strings.ToLower(providerName)

	if p, ok := simpleProviders[name]; ok {
		apiKey := os.Getenv(p.keyEnv)
		if apiKey == "" {
			return nil, fmt.Errorf("%s environment variable is not set", p.keyEnv)
		}

		return bifrost.New(bifrost.Config{
			Provider:         p.provider,
			APIKey:           apiKey,
			DefaultModel:     resolveModel(modelOverride, p.modelEnv, p.defaultModel),
			StreamingTimeout: timeout,
			ReasoningEnabled: p.reasoning,
		})
	}

	switch name {
	case "azure":
		apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
		if apiKey == "" {
			return nil, errors.New("AZURE_OPENAI_API_KEY environment variable is not set")
		}

		apiURL := os.Getenv("AZURE_OPENAI_ENDPOINT")
		if apiURL == "" {
			return nil, errors.New("AZURE_OPENAI_ENDPOINT environment variable is not set")
		}

		return bifrost.New(bifrost.Config{
			Provider:         schemas.Azure,
			APIKey:           apiKey,
			APIURL:           apiURL,
			DefaultModel:     resolveModel(modelOverride, "AZURE_OPENAI_MODEL", DefaultAzureModel),
			StreamingTimeout: timeout,
		})

	case "openaicompatible":
		apiURL := os.Getenv("OPENAI_COMPATIBLE_API_URL")
		if apiURL == "" {
			return nil, errors.New("OPENAI_COMPATIBLE_API_URL environment variable is not set")
		}

		model := resolveModel(modelOverride, "OPENAI_COMPATIBLE_MODEL", "")
		if model == "" {
			return nil, errors.New("OPENAI_COMPATIBLE_MODEL environment variable is not set")
		}

		return bifrost.New(bifrost.Config{
			Provider: schemas.OpenAI,
			// API key is optional for local LLMs
			APIKey:           os.Getenv("OPENAI_COMPATIBLE_API_KEY"),
			APIURL:           apiURL,
			DefaultModel:     model,
			StreamingTimeout: timeout,
		})

	case "bedrock":
		region := os.Getenv("AWS_BEDROCK_REGION")
		if region == "" {
			return nil, errors.New("AWS_BEDROCK_REGION environment variable is not set")
		}

		return bifrost.New(bifrost.Config{
			Provider:           schemas.Bedrock,
			Region:             region,
			AWSAccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
			AWSSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
			DefaultModel:       resolveModel(modelOverride, "AWS_BEDROCK_MODEL", DefaultBedrockModel),
			StreamingTimeout:   timeout,
		})

	default:
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
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
	provider, err := createProvider(providerName, "")
	if err != nil {
		return nil, err
	}

	// Setup grader LLM (separate from main LLM)
	graderLLM, err := createGraderLLM()
	if err != nil {
		return nil, fmt.Errorf("failed to create grader LLM: %w", err)
	}

	return &Eval{
		Prompts:   prompts,
		LLM:       provider,
		GraderLLM: graderLLM,
	}, nil
}

// createGraderLLM creates a separate LLM for grading based on environment variables.
// Defaults to OpenAI if not specified.
func createGraderLLM() (llm.LanguageModel, error) {
	graderProvider := os.Getenv("GRADER_LLM_PROVIDER")
	if graderProvider == "" {
		graderProvider = "openai"
	}

	graderModel := os.Getenv("GRADER_LLM_MODEL")
	if graderModel == "" && graderProvider == "openai" {
		graderModel = DefaultOpenAIModel
	}

	// Create grader provider with model override
	return createProvider(graderProvider, graderModel)
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
			e.T = t
			for i := range numEvals {
				e.runNumber = i
				f(e)
			}
		})
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
