// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/anthropic"
	"github.com/mattermost/mattermost-plugin-ai/bedrock"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
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

// createProvider creates an LLM provider based on the provider name
// Reads configuration from environment variables with optional model override
func createProvider(providerName string, modelOverride string) (llm.LanguageModel, error) {
	httpClient := &http.Client{}
	timeout := 20 * time.Second

	switch strings.ToLower(providerName) {
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, errors.New("OPENAI_API_KEY environment variable is not set")
		}

		model := modelOverride
		if model == "" {
			model = os.Getenv("OPENAI_MODEL")
			if model == "" {
				model = "gpt-5"
			}
		}

		provider := openai.New(openai.Config{
			APIKey:           apiKey,
			DefaultModel:     model,
			StreamingTimeout: timeout,
		}, httpClient)
		if provider == nil {
			return nil, errors.New("failed to create OpenAI provider")
		}
		return provider, nil

	case "anthropic":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, errors.New("ANTHROPIC_API_KEY environment variable is not set")
		}

		model := modelOverride
		if model == "" {
			model = os.Getenv("ANTHROPIC_MODEL")
			if model == "" {
				model = "claude-sonnet-4-5-20250929"
			}
		}

		provider := anthropic.New(llm.ServiceConfig{
			APIKey:       apiKey,
			DefaultModel: model,
		}, llm.BotConfig{
			ReasoningEnabled: true,
		}, httpClient)
		if provider == nil {
			return nil, errors.New("failed to create Anthropic provider")
		}
		return provider, nil

	case "azure":
		apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
		if apiKey == "" {
			return nil, errors.New("AZURE_OPENAI_API_KEY environment variable is not set")
		}

		apiURL := os.Getenv("AZURE_OPENAI_ENDPOINT")
		if apiURL == "" {
			return nil, errors.New("AZURE_OPENAI_ENDPOINT environment variable is not set")
		}

		model := modelOverride
		if model == "" {
			model = os.Getenv("AZURE_OPENAI_MODEL")
			if model == "" {
				model = "gpt-5"
			}
		}

		provider := openai.NewAzure(openai.Config{
			APIKey:           apiKey,
			APIURL:           apiURL,
			DefaultModel:     model,
			StreamingTimeout: timeout,
		}, httpClient)
		if provider == nil {
			return nil, errors.New("failed to create Azure OpenAI provider")
		}
		return provider, nil

	case "openaicompatible":
		apiURL := os.Getenv("OPENAI_COMPATIBLE_API_URL")
		if apiURL == "" {
			return nil, errors.New("OPENAI_COMPATIBLE_API_URL environment variable is not set")
		}

		model := modelOverride
		if model == "" {
			model = os.Getenv("OPENAI_COMPATIBLE_MODEL")
			if model == "" {
				return nil, errors.New("OPENAI_COMPATIBLE_MODEL environment variable is not set")
			}
		}

		// API key is optional for local LLMs
		apiKey := os.Getenv("OPENAI_COMPATIBLE_API_KEY")

		provider := openai.NewCompatible(openai.Config{
			APIKey:           apiKey,
			APIURL:           apiURL,
			DefaultModel:     model,
			StreamingTimeout: timeout,
		}, httpClient)
		if provider == nil {
			return nil, errors.New("failed to create OpenAI Compatible provider")
		}
		return provider, nil

	case "mistral":
		apiKey := os.Getenv("MISTRAL_API_KEY")
		if apiKey == "" {
			return nil, errors.New("MISTRAL_API_KEY environment variable is not set")
		}

		model := modelOverride
		if model == "" {
			model = os.Getenv("MISTRAL_MODEL")
			if model == "" {
				model = "mistral-large-latest"
			}
		}

		// Mistral uses an OpenAI-compatible API
		provider := openai.NewCompatible(openai.Config{
			APIKey:               apiKey,
			APIURL:               "https://api.mistral.ai/v1",
			DefaultModel:         model,
			StreamingTimeout:     timeout,
			DisableStreamOptions: true,
			UseMaxTokens:         true,
		}, httpClient)
		if provider == nil {
			return nil, errors.New("failed to create Mistral provider")
		}
		return provider, nil

	case "bedrock":
		region := os.Getenv("AWS_BEDROCK_REGION")
		if region == "" {
			return nil, errors.New("AWS_BEDROCK_REGION environment variable is not set")
		}

		model := modelOverride
		if model == "" {
			model = os.Getenv("AWS_BEDROCK_MODEL")
			if model == "" {
				model = "global.anthropic.claude-sonnet-4-20250514-v1:0"
			}
		}

		// AWS credentials are picked up from environment via standard AWS SDK chain:
		// AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY (and optionally AWS_SESSION_TOKEN)
		serviceConfig := llm.ServiceConfig{
			DefaultModel:       model,
			Region:             region,
			AWSAccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
			AWSSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		}

		provider, err := bedrock.New(serviceConfig, httpClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create Bedrock provider: %w", err)
		}
		return provider, nil

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

// createGraderLLM creates a separate LLM for grading based on environment variables
// Defaults to OpenAI with gpt-5 model if not specified
func createGraderLLM() (llm.LanguageModel, error) {
	// Get grader provider name from environment, default to "openai"
	graderProvider := os.Getenv("GRADER_LLM_PROVIDER")
	if graderProvider == "" {
		graderProvider = "openai"
	}

	// Get grader model override, default to gpt-5 for OpenAI
	graderModel := os.Getenv("GRADER_LLM_MODEL")
	if graderModel == "" && graderProvider == "openai" {
		graderModel = "gpt-5"
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
		providerName := providerName // Capture for closure

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
			for i := 0; i < numEvals; i++ {
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
		return []string{"openai", "anthropic", "azure", "mistral", "bedrock"}
	}

	// Handle comma-separated list
	if strings.Contains(providerEnv, ",") {
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

	// Single provider
	return []string{providerEnv}
}
