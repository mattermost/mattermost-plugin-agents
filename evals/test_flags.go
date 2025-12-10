// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"flag"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/anthropic"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
)

// Shared test flags across all bot evaluation tests
var (
	// Logging flags
	DebugFlag = flag.Bool("debug", false, "Enable debug logging - detailed operations + progress tracking (most verbose)")
	WarnFlag  = flag.Bool("warn", false, "Enable warn logging - high-level progress without detailed operations")

	// LLM configuration flags
	TemperatureFlag = flag.Float64("temperature", -1, "Set LLM temperature (0.0-1.0). Default uses model's default temperature")
	TimeoutFlag     = flag.Duration("timeout", 60*time.Second, "Set streaming timeout for LLM requests (e.g., 60s, 2m, 5m)")

	// Scenario and evaluation flags
	ScenarioFlag  = flag.String("scenarios", "ALL", "Run scenario subset: 'CORE' (PM only), 'BREADTH' (PM only), or 'ALL'")
	ThresholdFlag = flag.String("threshold", "MODERATE", "Rubric pass threshold: 'STRICT' (all pass), 'MODERATE' (majority), 'LAX' (minimal)")
	LevelFlag     = flag.String("level", "junior", "Skill level for scenarios: 'junior' or 'senior' (loads appropriate scenario files)")

	// Model and grading flags
	GraderModelFlag = flag.String("grader-model", "", "Override the model used for grading evaluations (default: same as test model)")

	// Baseline comparison flags
	RolePromptBaselineFlag = flag.Bool("role-prompt-baseline", false, "Use role-specific prompts for baseline (without data sources) instead of vanilla 'helpful assistant' prompt")

	// Grounding validation flags
	GroundingFlag = flag.Bool("grounding", false, "Enable grounding validation (citation and metadata analysis)")

	// Anthropic-specific flags
	DisableThinkingFlag = flag.Bool("disable-thinking", false, "Disable extended thinking for Anthropic models (reduces token usage and avoids tool use conflicts)")

	// Output and comparison flags
	SavePromptsFlag = flag.Bool("save-prompts", false, "Save baseline and enhanced prompts/outputs to files for analysis")
	SaveOutputDir   = flag.String("save-output-dir", "../../eval-comparison-output", "Directory to save prompts and outputs when -save-prompts is enabled")
	ComparisonMode  = flag.String("comparison-mode", "enhanced", "Comparison mode: 'baseline', 'enhanced', or 'both'")
)

// CreateLLMProvider creates an LLM provider for the given model name
// Routes to appropriate provider (Anthropic for Claude models, OpenAI for others)
func CreateLLMProvider(modelName string, streamingTimeout time.Duration, temperature *float32, httpClient *http.Client) llm.LanguageModel {
	modelName = strings.TrimSpace(modelName)

	// Route to appropriate provider based on model name
	if strings.HasPrefix(strings.ToLower(modelName), "claude-") {
		serviceConfig := llm.ServiceConfig{
			APIKey:       os.Getenv("ANTHROPIC_API_KEY"),
			DefaultModel: modelName,
			Type:         "anthropic",
		}
		botConfig := llm.BotConfig{
			ReasoningEnabled: !*DisableThinkingFlag,
		}
		return anthropic.New(serviceConfig, botConfig, httpClient)
	}

	// Special handling for mattermodel (local Ollama models)
	if strings.HasPrefix(strings.ToLower(modelName), "mattermodel") {
		config := openai.Config{
			APIKey:           "ollama",
			APIURL:           "http://localhost:11434/v1",
			DefaultModel:     modelName,
			StreamingTimeout: streamingTimeout,
		}

		if temperature != nil {
			config.DefaultTemperature = temperature
		}

		return openai.NewCompatible(config, httpClient)
	}

	// Use OpenAI for other models (default)
	config := openai.Config{
		APIKey:           os.Getenv("OPENAI_API_KEY"),
		DefaultModel:     modelName,
		StreamingTimeout: streamingTimeout,
	}

	if temperature != nil {
		config.DefaultTemperature = temperature
	}

	return openai.New(config, httpClient)
}

// GetModelsFromEnvOrDefault returns model list from TEST_MODEL env var or defaults
// Supports comma-separated list: TEST_MODEL="gpt-4o,claude-3-5-sonnet,mattermodel-5.4"
func GetModelsFromEnvOrDefault(defaultModels []string) []string {
	if envModel := os.Getenv("TEST_MODEL"); envModel != "" {
		models := strings.Split(envModel, ",")
		// Trim whitespace from each model name
		for i, modelName := range models {
			models[i] = strings.TrimSpace(modelName)
		}
		return models
	}
	return defaultModels
}
