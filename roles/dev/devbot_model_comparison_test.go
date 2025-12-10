// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dev_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/evals/baseline"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/mmtools"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/roles"
	"github.com/mattermost/mattermost-plugin-ai/roles/testutils"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDevBotModelComparison(t *testing.T) {
	// Load scenarios based on level flag
	scenarios, err := LoadDevBotScenarios(*levelFlag)
	if err != nil {
		t.Fatalf("Failed to load DevBot scenarios: %v", err)
	}

	// Filter scenarios if needed (for CORE/BREADTH/ALL subsets)
	scenarios = FilterScenariosByFlag(scenarios, *scenarioFlag, t)

	// Get models to compare
	models := getModelsForComparison()

	// Log comparison mode
	switch *comparisonMode {
	case "baseline":
		t.Logf("Comparing %d models in BASELINE mode (minimal prompts, no tools): %v", len(models), models)
	case "enhanced":
		t.Logf("Comparing %d models in ENHANCED mode (full DevBot with tools): %v", len(models), models)
	case "both":
		t.Logf("Comparing %d models in BOTH modes (baseline and enhanced separately): %v", len(models), models)
	default:
		t.Fatalf("Invalid comparison mode: %s. Must be 'baseline', 'enhanced', or 'both'", *comparisonMode)
	}

	// Create custom evaluation with devbot-specific configuration
	evalT, numEvals := createDevBotEval(t)

	// Calculate total runs for progress tracking
	totalRuns := len(scenarios) * len(models) * numEvals
	var currentRun int64 // Use atomic operations for thread-safe access

	// Setup shared services and tools outside model loop to enable cache sharing
	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	mmClient := mocks.NewMockClient(t)

	// Setup GetBundlePath mock expectation for datasources client
	mmClient.On("GetBundlePath").Return("", nil).Maybe()

	licenseChecker := enterprise.NewLicenseChecker(client)
	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil, nil)
	prompts, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err, "Failed to load prompts")

	// Create real config with full DevBot functionality (shared across models for cache efficiency)
	sharedLogger := createTestLogger(t, "shared")
	testConfig := CreateDevBotConfig(sharedLogger)
	configContainer := &config.Container{}
	configContainer.Update(testConfig)

	// Setup shared tool provider (this enables cache sharing between models)
	sharedToolProvider := mmtools.NewMMToolProvider(
		mmClient,
		nil, // search service - not needed for Dev tools in this test
		&http.Client{},
		configContainer,
		nil, // database - not needed for this test
	)
	mcpClientManager := &roles.MockMCPClientManager{}
	configProvider := &roles.MockConfigProvider{}

	// Run comparisons based on mode
	if *comparisonMode == "both" {
		// Run both baseline and enhanced comparisons
		t.Run("Baseline Comparison", func(t *testing.T) {
			runModelComparison(t, evalT, scenarios, models, totalRuns, "baseline", &currentRun,
				mockAPI, client, mmClient, licenseChecker, botService, prompts,
				sharedToolProvider, mcpClientManager, configProvider)
		})
		t.Run("Enhanced Comparison", func(t *testing.T) {
			runModelComparison(t, evalT, scenarios, models, totalRuns, "enhanced", &currentRun,
				mockAPI, client, mmClient, licenseChecker, botService, prompts,
				sharedToolProvider, mcpClientManager, configProvider)
		})
	} else {
		// Run single mode comparison
		runModelComparison(t, evalT, scenarios, models, totalRuns, *comparisonMode, &currentRun,
			mockAPI, client, mmClient, licenseChecker, botService, prompts,
			sharedToolProvider, mcpClientManager, configProvider)
	}
}

// runModelComparison runs comparison for a specific mode (baseline or enhanced)
func runModelComparison(
	t *testing.T,
	evalT *evals.EvalT,
	scenarios []baseline.Scenario,
	models []string,
	totalRuns int,
	mode string,
	currentRun *int64,
	mockAPI *plugintest.API,
	client *pluginapi.Client,
	mmClient *mocks.MockClient,
	licenseChecker *enterprise.LicenseChecker,
	botService *bots.MMBots,
	prompts *llm.Prompts,
	sharedToolProvider interface{}, // Actually *mmtools.MMToolProvider but avoiding import
	mcpClientManager *roles.MockMCPClientManager,
	configProvider *roles.MockConfigProvider,
) {
	numEvals := evals.NumEvalsOrSkip(t)

	for scenarioIdx, scenario := range scenarios {
		testName := fmt.Sprintf("devbot %s comparison %s", mode, scenario.Name)
		t.Run(testName, func(t *testing.T) {
			evalT.T = t
			for trialIdx := range numEvals {
				func(t *evals.EvalT) {
					trialNum := trialIdx + 1
					// Store all model responses for comparison
					modelResponses := make(map[string]string)
					modelEvaluations := make(map[string]*ThresholdEvaluationResult)

					// Test the scenario with each model
					var wg sync.WaitGroup
					var mu sync.Mutex // For safe access to maps

					for modelIdx, modelName := range models {
						wg.Add(1)
						go func(modelIdx int, modelName string) {
							defer wg.Done()

							runNum := atomic.AddInt64(currentRun, 1)
							modeLabel := strings.ToUpper(mode)
							t.Logf("🔄 RUN %d/%d - TRIAL %d/%d [SCENARIO %d/%d]: Testing %s model %d/%d (%s) on scenario '%s'",
								runNum, totalRuns, trialNum, numEvals, scenarioIdx+1, len(scenarios), modeLabel, modelIdx+1, len(models), modelName, scenario.Name)
							t.Logf("TRIAL %d/%d [%s-%s]: Query: %s", trialNum, numEvals, modelName, modeLabel, testutils.TruncateString(scenario.Message, 150))

							// Setup mock expectations early before any LLM operations
							mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

							// Create model-specific LLM provider
							streamingTimeout := *timeoutFlag
							if streamingTimeout <= 0 {
								streamingTimeout = 60 * time.Second
							}
							var temperature *float32
							if *temperatureFlag >= 0 {
								temp := float32(*temperatureFlag)
								temperature = &temp
							}
							httpClient := &http.Client{
								Timeout: streamingTimeout * 2,
							}
							modelLLM := createLLMProvider(modelName, streamingTimeout, temperature, httpClient)
							require.NotNil(t.T, modelLLM, "Failed to create LLM provider for model %s", modelName)

							var response string
							var err error

							if mode == "baseline" {
								// ============ RUN BASELINE VERSION ============
								t.Logf("TRIAL %d/%d [%s-BASELINE]: Running baseline (minimal prompt, no tools)...", trialNum, numEvals, modelName)

								// Create baseline bot with minimal prompt
								baselineRequest := llm.CompletionRequest{
									Posts: []llm.Post{
										{
											Role:    llm.PostRoleSystem,
											Message: "You are a helpful AI assistant that helps developers with Mattermost plugin development.",
										},
										{
											Role:    llm.PostRoleUser,
											Message: scenario.Message,
										},
									},
									Context: llm.NewContext(),
								}

								response, err = modelLLM.ChatCompletionNoStream(baselineRequest)
								require.NoError(t, err, "Failed to get baseline response for model %s", modelName)
								t.Logf("TRIAL %d/%d [%s-BASELINE]: Response received (%d chars)", trialNum, numEvals, modelName, len(response))
							} else {
								// ============ RUN ENHANCED DEVBOT VERSION ============
								t.Logf("TRIAL %d/%d [%s-ENHANCED]: Running enhanced DevBot with tools...", trialNum, numEvals, modelName)

								// Create thread data for Dev conversation
								threadData := CreateDevBotThreadData(scenario.Message)

								// Setup mock expectations
								mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
								mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "professional"}).Maybe()
								mockAPI.On("GetTeam", threadData.Team.Id).Return(threadData.Team, nil)
								mockAPI.On("GetChannel", threadData.Channel.Id).Return(threadData.Channel, nil)
								mmClient.On("GetPostThread", threadData.LatestPost().Id).Return(threadData.PostList, nil).Maybe()
								mmClient.On("GetChannel", threadData.Channel.Id).Return(threadData.Channel, nil).Maybe()
								mmClient.On("LogDebug", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
									if *warnFlag {
										msg := args.Get(0).(string)
										if len(args) > 1 {
											if fields, ok := args.Get(1).([]interface{}); ok && len(fields) >= 2 {
												// Look for LLM request/response and cache hits/misses and datasource calls
												if msg == "LLM Request" || msg == "LLM Response" {
													// Handle LLM logging from LanguageModelVerboseTestLogWrapper
													for i := 0; i < len(fields)-1; i += 2 {
														if key, ok := fields[i].(string); ok && key == "content" {
															if content, ok := fields[i+1].(string); ok {
																t.Logf("LLM[%s]: %s", modelName, content)
															}
														}
													}
													return
												}

												// Extract structured fields from log message
												logFields := make(map[string]interface{})
												for i := 0; i < len(fields)-1; i += 2 {
													if key, ok := fields[i].(string); ok {
														logFields[key] = fields[i+1]
													}
												}

												// Extract common fields
												source := getStringField(logFields, "source")
												topic := getStringField(logFields, "topic")
												docCount := getIntField(logFields, "docs")
												duration := getIntField(logFields, "duration_ms")
												errorMsg := getStringField(logFields, "error")

												// Get or create topic ID for cleaner logging, log description once
												topicID := getTopicDisplayWithDescription(topic, t.T)

												// Generate enhanced logging based on message type
												switch {
												case strings.Contains(msg, "External docs cache hit"):
													t.Logf("CACHE HIT[%s]: source=%s returned %d docs for topic=%s", modelName, source, docCount, topicID)
												case strings.Contains(msg, "External docs fuzzy cache hit"):
													t.Logf("CACHE FUZZY HIT[%s]: source=%s returned %d docs for similar topic=%s", modelName, source, docCount, topicID)
												case strings.Contains(msg, "External docs cache miss"):
													t.Logf("CACHE MISS[%s]: source=%s, topic=%s", modelName, source, topicID)
												case strings.Contains(msg, "External docs cached results"):
													t.Logf("DATASOURCE[%s]: source=%s fetched %d docs in %dms for topic=%s", modelName, source, docCount, duration, topicID)
												case strings.Contains(msg, "External docs not caching empty results"):
													t.Logf("DATASOURCE[%s]: source=%s returned 0 docs in %dms for topic=%s (not cached)", modelName, source, duration, topicID)
												case strings.Contains(msg, "External docs fetch failed"):
													t.Logf("DATASOURCE ERROR[%s]: source=%s failed in %dms for topic=%s - %s", modelName, source, duration, topicID, errorMsg)
												case strings.Contains(msg, "HTTP protocol REJECTION:"):
													t.Logf("HTTP REJECTION[%s]: %s", modelName, msg)
												case strings.Contains(msg, "QUALITY DEBUG:"):
													if strings.Contains(msg, "FAIL") || strings.Contains(msg, "FAILED") {
														t.Logf("QUALITY FAIL[%s]: %s", modelName, msg)
													}
												case strings.Contains(msg, "GitHub protocol") || strings.Contains(msg, "GitHub code search") || strings.Contains(msg, "GitHub issues") || strings.Contains(msg, "GitHub PRs"):
													t.Logf("GITHUB DEBUG[%s]: %s", modelName, msg)
												}
											}
										}
									}
								}).Maybe()
								mmClient.On("LogWarn", mock.Anything, mock.Anything).Return().Maybe()
								mmClient.On("GetPluginStatus", mock.Anything).Return(&model.PluginStatus{PluginId: "test", State: model.PluginStateRunning}, nil).Maybe()

								for _, user := range threadData.Users {
									mmClient.On("GetUser", user.Id).Return(user, nil).Maybe()
								}
								for _, fileInfo := range threadData.FileInfos {
									mmClient.On("GetFileInfo", fileInfo.Id).Return(fileInfo, nil).Maybe()
								}
								for id, file := range threadData.Files {
									mmClient.On("GetFile", id).Return(io.NopCloser(bytes.NewReader(file)), nil).Maybe()
								}

								// Create real config with full DevBot functionality
								modelLogger := createTestLogger(t.T, modelName)
								testConfig := CreateDevBotConfig(modelLogger)

								configContainer := &config.Container{}
								configContainer.Update(testConfig)

								// Use shared tool provider instead of creating a new one
								toolProvider, ok := sharedToolProvider.(llmcontext.ToolProvider)
								if !ok {
									t.Errorf("sharedToolProvider does not implement llmcontext.ToolProvider")
									return
								}
								contextBuilder := llmcontext.NewLLMContextBuilder(
									client,
									toolProvider,
									mcpClientManager,
									configProvider,
								)

								conv := conversations.New(
									prompts,
									mmClient,
									nil,
									contextBuilder,
									botService,
									nil,
									licenseChecker,
									i18n.Init(),
									nil,
								)

								// Create DevBot with specific model
								bot := CreateDevBot(modelName, "devbotid")

								// Set LLM
								bot.SetLLMForTest(modelLLM)

								// Process the enhanced DevBot request
								textStream, err := conv.ProcessUserRequest(bot, threadData.RequestingUser(), threadData.Channel, threadData.LatestPost())
								require.NoError(t.T, err, "Failed to process enhanced DevBot request with model %s", modelName)
								require.NotNil(t.T, textStream, "Expected a non-nil text stream for model %s", modelName)

								// Read the enhanced response with proper tool handling
								response, err = ProcessStreamWithTools(t.T, textStream, threadData.LatestPost(), contextBuilder, bot, threadData, conv)
								require.NoError(t.T, err, "Failed to read enhanced response from text stream for model %s", modelName)
								assert.NotEmpty(t.T, response, "Expected a non-empty enhanced DevBot response for model %s", modelName)

								t.Logf("TRIAL %d/%d [%s-ENHANCED]: Response received (%d chars)", trialNum, numEvals, modelName, len(response))
							}

							// ============ EVALUATE RESPONSE ============
							modeUpperLabel := strings.ToUpper(mode)
							t.Logf("TRIAL %d/%d [%s-%s]: Evaluating response...", trialNum, numEvals, modelName, modeUpperLabel)

							// Evaluate response
							logPrefix := fmt.Sprintf("TRIAL %d/%d [%s-%s]", trialNum, numEvals, modelName, modeUpperLabel)
							evalResult, evalErrors := EvaluateRubricsWithThreshold(
								t,
								scenario.Rubrics,
								response,
								*thresholdFlag,
								trialNum,
								numEvals,
								logPrefix,
							)

							// Store results safely
							mu.Lock()
							modelResponses[modelName] = response
							modelEvaluations[modelName] = evalResult
							mu.Unlock()

							// Check for evaluation errors
							if len(evalErrors) > 0 {
								for _, err := range evalErrors {
									t.Errorf("%s: %s", logPrefix, err)
								}
								return
							}

							// Log individual model result
							t.Logf("Model %s (%s): %d/%d rubrics passed (%.1f%%)",
								modelName, modeUpperLabel, evalResult.PassedRubrics, evalResult.TotalRubrics,
								float64(evalResult.PassedRubrics)*100/float64(evalResult.TotalRubrics))

							if *debugFlag {
								t.Logf("DEBUG: TRIAL %d/%d [%s-%s]: Response length: %d characters", trialNum, numEvals, modelName, modeUpperLabel, len(response))
								preview := testutils.TruncateString(response, 200)
								t.Logf("DEBUG: TRIAL %d/%d [%s-%s]: Response preview: %s", trialNum, numEvals, modelName, modeUpperLabel, preview)
							}
						}(modelIdx, modelName)
					}
					// Wait for all models to complete for this trial
					wg.Wait()

					// ============ COMPARE ALL MODELS ============
					if len(modelEvaluations) > 1 {
						t.Logf("========== %s MODEL COMPARISON RESULTS ==========", strings.ToUpper(mode))
						t.Logf("Scenario: %s | Trial: %d/%d | Mode: %s", scenario.Name, trialNum, numEvals, strings.ToUpper(mode))

						// Find best performing model(s)
						maxScore := 0
						var bestModels []string
						for modelName, eval := range modelEvaluations {
							if eval.PassedRubrics > maxScore {
								maxScore = eval.PassedRubrics
								bestModels = []string{modelName}
							} else if eval.PassedRubrics == maxScore {
								bestModels = append(bestModels, modelName)
							}
						}

						// Sort models by performance for consistent output
						type modelPerformance struct {
							name   string
							passed int
							total  int
						}
						var performances []modelPerformance
						for modelName, eval := range modelEvaluations {
							performances = append(performances, modelPerformance{
								name:   modelName,
								passed: eval.PassedRubrics,
								total:  eval.TotalRubrics,
							})
						}
						// Sort by passed rubrics (descending), then by name (ascending) for deterministic ordering
						for i := 0; i < len(performances); i++ {
							for j := i + 1; j < len(performances); j++ {
								if performances[j].passed > performances[i].passed ||
									(performances[j].passed == performances[i].passed && performances[j].name < performances[i].name) {
									performances[i], performances[j] = performances[j], performances[i]
								}
							}
						}

						// Display results
						for i, perf := range performances {
							percentage := float64(perf.passed) * 100 / float64(perf.total)
							rankEmoji := ""
							switch i {
							case 0:
								if len(bestModels) == 1 {
									rankEmoji = "🥇"
								} else {
									rankEmoji = "🏆"
								}
							case 1:
								switch {
								case len(bestModels) == 1:
									rankEmoji = "🥈"
								case len(bestModels) > 1 && performances[0].passed == perf.passed:
									rankEmoji = "🏆"
								default:
									rankEmoji = "🥈"
								}
							case 2:
								switch {
								case len(bestModels) == 1 && performances[1].passed != perf.passed:
									rankEmoji = "🥉"
								case len(bestModels) > 2 && performances[0].passed == perf.passed:
									rankEmoji = "🏆"
								case performances[1].passed == perf.passed:
									rankEmoji = "🥈"
								default:
									rankEmoji = "🥉"
								}
							}
							t.Logf("%s %s: %d/%d rubrics passed (%.1f%%)",
								rankEmoji, perf.name, perf.passed, perf.total, percentage)
						}

						// Show pairwise comparisons
						if len(models) == 2 {
							model1, model2 := models[0], models[1]
							eval1, eval2 := modelEvaluations[model1], modelEvaluations[model2]
							diff := eval1.PassedRubrics - eval2.PassedRubrics

							switch {
							case diff > 0:
								t.Logf("⬆️ %s performs better than %s by +%d rubrics", model1, model2, diff)
							case diff < 0:
								t.Logf("⬆️ %s performs better than %s by +%d rubrics", model2, model1, -diff)
							default:
								t.Logf("➖ %s and %s perform equally", model1, model2)
							}
						}

						t.Logf("===============================================")
					}
				}(evalT)
			}
		})
	}
}

// getModelsForComparison returns the list of models to compare
func getModelsForComparison() []string {
	return evals.GetModelsFromEnvOrDefault([]string{
		"mattermodel-5.4",
		"gpt-4o",
		"gpt-4o-mini",
	})
}
