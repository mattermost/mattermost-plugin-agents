// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/require"
)

// bridgeServiceConfig builds a valid, completion-capable service configuration
// of the given type. Credentials are filled in per type so these tests run
// against the real llm.IsValidService and bifrost support rules instead of a
// hand-rolled notion of eligibility.
func bridgeServiceConfig(id, name, serviceType, defaultModel string) llm.ServiceConfig {
	svc := llm.ServiceConfig{
		ID:           id,
		Name:         name,
		Type:         serviceType,
		DefaultModel: defaultModel,
	}
	switch serviceType {
	case llm.ServiceTypeOpenAICompatible:
		svc.APIURL = "https://compatible.example.com/v1"
	case llm.ServiceTypeScale:
		svc.APIKey = "test-key"
		svc.APIURL = "https://scale.example.com"
	default:
		svc.APIKey = "test-key"
	}
	return svc
}

// serviceLLMAcquireCall records one lease request the handler made.
type serviceLLMAcquireCall struct {
	svc       llm.ServiceConfig
	fallbacks []llm.ServiceConfig
}

// recordingServiceLLMAcquirer stands in for the bots service LLM registry. It
// records what the handler asked for, counts lease releases, and wraps the
// recording FakeLLM in the same structured-output fallback wrapper the
// production builder installs, so the service structured-output policy is
// exercised end to end through the HTTP handler.
type recordingServiceLLMAcquirer struct {
	fake *FakeLLM
	// err, when set, makes every acquisition fail.
	err error

	mu       sync.Mutex
	calls    []serviceLLMAcquireCall
	releases int
}

func (r *recordingServiceLLMAcquirer) acquire(svc llm.ServiceConfig, fallbacks []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
	r.mu.Lock()
	r.calls = append(r.calls, serviceLLMAcquireCall{svc: svc, fallbacks: fallbacks})
	r.mu.Unlock()

	if r.err != nil {
		return nil, nil, r.err
	}

	primary := llm.StructuredOutputTarget{Service: svc, Model: svc.DefaultModel}
	fallbackTargets := make([]llm.StructuredOutputTarget, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		fallbackTargets = append(fallbackTargets, llm.StructuredOutputTarget{
			Service: fallback,
			Model:   fallback.DefaultModel,
		})
	}
	model := llm.NewStructuredOutputFallbackWrapper(r.fake, primary, fallbackTargets, bifrost.ResolveStructuredOutputCapability)

	release := func() {
		r.mu.Lock()
		r.releases++
		r.mu.Unlock()
	}
	return model, release, nil
}

func (r *recordingServiceLLMAcquirer) acquireCalls() []serviceLLMAcquireCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]serviceLLMAcquireCall(nil), r.calls...)
}

func (r *recordingServiceLLMAcquirer) releaseCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.releases
}

// installServiceLLM wires a recording acquirer serving the given fake model
// into the API under test.
func (e *TestEnvironment) installServiceLLM(fake *FakeLLM) *recordingServiceLLMAcquirer {
	acquirer := &recordingServiceLLMAcquirer{fake: fake}
	e.api.SetServiceLLMAcquirerForTest(acquirer.acquire)
	return acquirer
}

// doBridgeRequest performs a raw inter-plugin bridge request so tests can send
// values the bridgeclient rejects client-side (such as a malformed user_id).
func (e *TestEnvironment) doBridgeRequest(t *testing.T, method, path, body string) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Mattermost-Plugin-ID", "test-plugin")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)
	return recorder.Result()
}

func bridgeServiceIDs(services []bridgeclient.BridgeServiceInfo) []string {
	ids := make([]string, 0, len(services))
	for _, svc := range services {
		ids = append(ids, svc.ID)
	}
	return ids
}

// TestBridgeGetServicesFromConfiguration verifies discovery reads stored
// service configuration directly: no agent has to exist, ineligible services
// are omitted, and duplicates resolve to the first configured entry.
func TestBridgeGetServicesFromConfiguration(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		services    []llm.ServiceConfig
		expectedIDs []string
		validateRes func(t *testing.T, services []bridgeclient.BridgeServiceInfo)
	}{
		{
			name:        "no services configured",
			services:    nil,
			expectedIDs: []string{},
		},
		{
			name: "eligible services are listed with id, name and type",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			expectedIDs: []string{"svc-openai"},
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Equal(t, bridgeclient.BridgeServiceInfo{
					ID:   "svc-openai",
					Name: "OpenAI",
					Type: llm.ServiceTypeOpenAI,
				}, services[0])
			},
		},
		{
			name: "sorted by name then id",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-z", "Zulu", llm.ServiceTypeOpenAI, "gpt-4o"),
				bridgeServiceConfig("svc-b", "Shared", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
				bridgeServiceConfig("svc-a", "Shared", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
				bridgeServiceConfig("svc-alpha", "Alpha", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			expectedIDs: []string{"svc-alpha", "svc-a", "svc-b", "svc-z"},
		},
		{
			name: "invalid service is excluded",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-ok", "OK", llm.ServiceTypeOpenAI, "gpt-4o"),
				// OpenAI without an API key does not validate.
				{ID: "svc-invalid", Name: "Invalid", Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-4o"},
			},
			expectedIDs: []string{"svc-ok"},
		},
		{
			name: "service type with no provider mapping is excluded",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-ok", "OK", llm.ServiceTypeOpenAI, "gpt-4o"),
				bridgeServiceConfig("svc-scale", "Scale", llm.ServiceTypeScale, "scale-model"),
			},
			expectedIDs: []string{"svc-ok"},
		},
		{
			name: "service without a default model is excluded",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-ok", "OK", llm.ServiceTypeOpenAI, "gpt-4o"),
				bridgeServiceConfig("svc-modelless", "Modelless", llm.ServiceTypeOpenAI, ""),
			},
			expectedIDs: []string{"svc-ok"},
		},
		{
			name: "service with a broken fallback chain is excluded",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-ok", "OK", llm.ServiceTypeOpenAI, "gpt-4o"),
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc-dangling", "Dangling", llm.ServiceTypeOpenAI, "gpt-4o")
					svc.FallbackServiceID = "svc-missing"
					return svc
				}(),
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc-bad-fallback", "BadFallback", llm.ServiceTypeOpenAI, "gpt-4o")
					svc.FallbackServiceID = "svc-scale"
					return svc
				}(),
				bridgeServiceConfig("svc-scale", "Scale", llm.ServiceTypeScale, "scale-model"),
			},
			expectedIDs: []string{"svc-ok"},
		},
		{
			name: "service with a usable fallback chain is listed",
			services: []llm.ServiceConfig{
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc-primary", "Primary", llm.ServiceTypeOpenAI, "gpt-4o")
					svc.FallbackServiceID = "svc-backup"
					return svc
				}(),
				bridgeServiceConfig("svc-backup", "Backup", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
			},
			expectedIDs: []string{"svc-backup", "svc-primary"},
		},
		{
			name: "blank-named service is listed and sorts first",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-named", "Named", llm.ServiceTypeOpenAI, "gpt-4o"),
				bridgeServiceConfig("svc-blank", "", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			expectedIDs: []string{"svc-blank", "svc-named"},
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Empty(t, services[0].Name)
			},
		},
		{
			name: "duplicate ids resolve to the first configured entry",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-dup", "First", llm.ServiceTypeOpenAI, "gpt-4o"),
				bridgeServiceConfig("svc-dup", "Second", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
			},
			expectedIDs: []string{"svc-dup"},
			validateRes: func(t *testing.T, services []bridgeclient.BridgeServiceInfo) {
				require.Equal(t, "First", services[0].Name)
				require.Equal(t, llm.ServiceTypeOpenAI, services[0].Type)
			},
		},
		{
			name: "an ineligible first entry does not let a duplicate id take its place",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-dup", "First", llm.ServiceTypeOpenAI, ""),
				bridgeServiceConfig("svc-dup", "Second", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			expectedIDs: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.config.services = tc.services

			// No agents at all: services are discoverable on their own.
			require.Empty(t, e.bots.GetAllBots())

			client := e.CreateBridgeClient()
			services, err := client.GetServices("")
			require.NoError(t, err)

			require.Equal(t, tc.expectedIDs, bridgeServiceIDs(services))
			if tc.validateRes != nil {
				tc.validateRes(t, services)
			}
		})
	}
}

// TestBridgeGetServicesIgnoresUserID verifies user_id is still syntax-validated
// on the discovery route but no longer filters the list: services are not
// agent-scoped, so an agent whose ACLs reject the user cannot hide one.
func TestBridgeGetServicesIgnoresUserID(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	setup := func(t *testing.T) *TestEnvironment {
		t.Helper()
		e := SetupTestEnvironment(t)
		e.config.services = []llm.ServiceConfig{
			bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
			bridgeServiceConfig("svc-anthropic", "Anthropic", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
		}
		// An agent that blocks the requesting user must not affect the list.
		e.setupTestBot(llm.BotConfig{
			Name:            "blocking",
			DisplayName:     "Blocking Agent",
			UserAccessLevel: llm.UserAccessLevelBlock,
			UserIDs:         []string{testUserID},
		})
		return e
	}

	t.Run("same list with and without user_id", func(t *testing.T) {
		e := setup(t)
		defer e.Cleanup(t)

		client := e.CreateBridgeClient()

		withoutUser, err := client.GetServices("")
		require.NoError(t, err)
		withUser, err := client.GetServices(testUserID)
		require.NoError(t, err)

		require.Equal(t, []string{"svc-anthropic", "svc-openai"}, bridgeServiceIDs(withoutUser))
		require.Equal(t, withoutUser, withUser)
	})

	t.Run("malformed user_id is rejected", func(t *testing.T) {
		e := setup(t)
		defer e.Cleanup(t)

		resp := e.doBridgeRequest(t, http.MethodGet, "/bridge/v1/services?user_id=bad", "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "invalid user_id")
	})
}

// serviceCompletionInvoker runs a service completion through one of the two
// endpoints so every behavior is covered on both the streaming and
// non-streaming paths.
type serviceCompletionInvoker struct {
	name string
	call func(client *bridgeclient.Client, service string, req bridgeclient.CompletionRequest) (string, error)
}

func serviceCompletionInvokers() []serviceCompletionInvoker {
	return []serviceCompletionInvoker{
		{
			name: "nostream",
			call: func(client *bridgeclient.Client, service string, req bridgeclient.CompletionRequest) (string, error) {
				return client.ServiceCompletion(service, req)
			},
		},
		{
			name: "streaming",
			call: func(client *bridgeclient.Client, service string, req bridgeclient.CompletionRequest) (string, error) {
				result, err := client.ServiceCompletionStream(service, req)
				if err != nil {
					return "", err
				}
				return result.ReadAll()
			},
		},
	}
}

// TestBridgeServiceCompletionResolvesStoredService covers how the `:service`
// path value maps onto stored configuration, and that the resolved primary and
// its fallback chain are what the language model is leased for.
func TestBridgeServiceCompletionResolvesStoredService(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name              string
		services          []llm.ServiceConfig
		service           string
		expectedSvcID     string
		expectedFallbacks []string
		expectedErr       string
	}{
		{
			name: "by id",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			service:       "svc-openai",
			expectedSvcID: "svc-openai",
		},
		{
			name: "by name",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			service:       "OpenAI",
			expectedSvcID: "svc-openai",
		},
		{
			name: "id match wins over another service's matching name",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-b", "svc-a", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
				bridgeServiceConfig("svc-a", "Alpha", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			service:       "svc-a",
			expectedSvcID: "svc-a",
		},
		{
			name: "duplicate ids resolve to the first configured entry",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-dup", "First", llm.ServiceTypeOpenAI, "gpt-4o"),
				bridgeServiceConfig("svc-dup", "Second", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
			},
			service:       "svc-dup",
			expectedSvcID: "svc-dup",
		},
		{
			name: "duplicate names resolve to the first configured entry",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-first", "Shared", llm.ServiceTypeOpenAI, "gpt-4o"),
				bridgeServiceConfig("svc-second", "Shared", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
			},
			service:       "Shared",
			expectedSvcID: "svc-first",
		},
		{
			name: "blank-named service is callable by id",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-blank", "", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			service:       "svc-blank",
			expectedSvcID: "svc-blank",
		},
		{
			name: "fallback chain is resolved from the same snapshot",
			services: []llm.ServiceConfig{
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc-primary", "Primary", llm.ServiceTypeOpenAI, "gpt-4o")
					svc.FallbackServiceID = "svc-backup"
					return svc
				}(),
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc-backup", "Backup", llm.ServiceTypeAnthropic, "claude-sonnet-4-5")
					svc.FallbackServiceID = "svc-last"
					return svc
				}(),
				bridgeServiceConfig("svc-last", "Last", llm.ServiceTypeGemini, "gemini-2.5-pro"),
			},
			service:           "Primary",
			expectedSvcID:     "svc-primary",
			expectedFallbacks: []string{"svc-backup", "svc-last"},
		},
		{
			name: "unknown value is not found",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			service:     "nonexistent-service",
			expectedErr: "service not found: nonexistent-service",
		},
		{
			name: "ineligible service is not found",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc-scale", "Scale", llm.ServiceTypeScale, "scale-model"),
			},
			service:     "svc-scale",
			expectedErr: "service not found: svc-scale",
		},
		{
			name: "eligible service with a broken fallback chain fails as a server error",
			services: []llm.ServiceConfig{
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc-primary", "Primary", llm.ServiceTypeOpenAI, "gpt-4o")
					svc.FallbackServiceID = "svc-missing"
					return svc
				}(),
			},
			service:     "svc-primary",
			expectedErr: `service "svc-primary" is not usable`,
		},
	}

	for _, invoker := range serviceCompletionInvokers() {
		for _, tc := range tests {
			t.Run(invoker.name+"/"+tc.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.config.services = tc.services
				require.Empty(t, e.bots.GetAllBots(), "the service path must work with no agents configured")

				acquirer := e.installServiceLLM(NewFakeLLM("service response"))

				client := e.CreateBridgeClient()
				result, err := invoker.call(client, tc.service, bridgeclient.CompletionRequest{
					Posts: []bridgeclient.Post{{Role: "user", Message: "Hello"}},
				})

				if tc.expectedErr != "" {
					require.Error(t, err)
					require.Contains(t, err.Error(), tc.expectedErr)
					return
				}

				require.NoError(t, err)
				require.Equal(t, "service response", result)

				calls := acquirer.acquireCalls()
				require.Len(t, calls, 1)

				expectedSvc, ok := resolveBridgeService(tc.services, tc.service)
				require.True(t, ok)
				require.Equal(t, tc.expectedSvcID, expectedSvc.ID)
				require.Equal(t, expectedSvc, calls[0].svc)

				fallbackIDs := make([]string, 0, len(calls[0].fallbacks))
				for _, fallback := range calls[0].fallbacks {
					fallbackIDs = append(fallbackIDs, fallback.ID)
				}
				require.Equal(t, tc.expectedFallbacks, nilIfEmpty(fallbackIDs))

				require.Equal(t, 1, acquirer.releaseCount(), "the lease must be released exactly once")
			})
		}
	}
}

func nilIfEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

// TestBridgeServiceCompletionSkipsAgentPermissionChecks verifies the service
// endpoints no longer borrow an agent's ACLs: a request naming a user an
// existing agent would reject still succeeds, because the service path has no
// agent and inter-plugin trust is the boundary.
func TestBridgeServiceCompletionSkipsAgentPermissionChecks(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, invoker := range serviceCompletionInvokers() {
		t.Run(invoker.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.config.services = []llm.ServiceConfig{
				bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
			}

			// This agent would reject the user on the agent endpoints, and its
			// channel access level would reject the channel too.
			e.setupTestBot(llm.BotConfig{
				Name:               "blocking",
				DisplayName:        "Blocking Agent",
				ServiceID:          "svc-openai",
				UserAccessLevel:    llm.UserAccessLevelBlock,
				UserIDs:            []string{testUserID},
				ChannelAccessLevel: llm.ChannelAccessLevelBlock,
				ChannelIDs:         []string{testChannelID},
			})

			e.mockAPI.On("GetChannel", testChannelID).Return(&model.Channel{
				Id:     testChannelID,
				Type:   model.ChannelTypeOpen,
				TeamId: "team-bridge",
			}, nil).Maybe()

			e.installServiceLLM(NewFakeLLM("allowed"))

			client := e.CreateBridgeClient()
			result, err := invoker.call(client, "svc-openai", bridgeclient.CompletionRequest{
				Posts:     []bridgeclient.Post{{Role: "user", Message: "Hello"}},
				UserID:    testUserID,
				ChannelID: testChannelID,
			})
			require.NoError(t, err)
			require.Equal(t, "allowed", result)
		})
	}
}

// TestBridgeServiceCompletionAttribution verifies user, channel and team
// attribution still reach the LLM context while bot fields stay empty: the
// service path has no agent identity to report.
func TestBridgeServiceCompletionAttribution(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name            string
		channel         *model.Channel
		channelErr      *model.AppError
		expectedTeamID  string
		expectChannelID string
	}{
		{
			name: "channel in a team carries team attribution",
			channel: &model.Channel{
				Id:     testChannelID,
				Type:   model.ChannelTypeOpen,
				TeamId: "team-bridge",
			},
			expectedTeamID:  "team-bridge",
			expectChannelID: testChannelID,
		},
		{
			name: "direct channel carries no team attribution",
			channel: &model.Channel{
				Id:     testChannelID,
				Type:   model.ChannelTypeDirect,
				TeamId: "team-bridge",
			},
			expectChannelID: testChannelID,
		},
		{
			name:            "channel lookup failure degrades to the channel id",
			channelErr:      &model.AppError{Message: "boom"},
			expectChannelID: testChannelID,
		},
	}

	for _, invoker := range serviceCompletionInvokers() {
		for _, tc := range tests {
			t.Run(invoker.name+"/"+tc.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.config.services = []llm.ServiceConfig{
					bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
				}

				if tc.channelErr != nil {
					e.mockAPI.On("GetChannel", testChannelID).Return(nil, tc.channelErr)
				} else {
					e.mockAPI.On("GetChannel", testChannelID).Return(tc.channel, nil)
				}

				fake := NewFakeLLM("attributed")
				e.installServiceLLM(fake)

				client := e.CreateBridgeClient()
				_, err := invoker.call(client, "svc-openai", bridgeclient.CompletionRequest{
					Posts:     []bridgeclient.Post{{Role: "user", Message: "Hello"}},
					UserID:    testUserID,
					ChannelID: testChannelID,
				})
				require.NoError(t, err)

				llmContext := fake.LastRequest().Context
				require.NotNil(t, llmContext)
				require.NotNil(t, llmContext.RequestingUser)
				require.Equal(t, testUserID, llmContext.RequestingUser.Id)
				require.NotNil(t, llmContext.Channel)
				require.Equal(t, tc.expectChannelID, llmContext.Channel.Id)
				if tc.expectedTeamID == "" {
					require.Nil(t, llmContext.Team)
				} else {
					require.NotNil(t, llmContext.Team)
					require.Equal(t, tc.expectedTeamID, llmContext.Team.Id)
				}

				require.Empty(t, llmContext.BotName)
				require.Empty(t, llmContext.BotUsername)
				require.Empty(t, llmContext.BotUserID)
				require.Empty(t, llmContext.BotModel)
				require.Empty(t, llmContext.BotServiceType)
				require.Empty(t, llmContext.CustomInstructions)
			})
		}
	}
}

// bridgeJSONSchema is a small object schema used to request structured output.
var bridgeJSONSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"answer": map[string]interface{}{"type": "string"},
	},
}

// systemPostContainsJSONInstruction reports whether the request carries the
// prompt-level JSON instruction the structured-output fallback injects.
func systemPostContainsJSONInstruction(request llm.CompletionRequest) bool {
	for _, post := range request.Posts {
		if post.Role == llm.PostRoleSystem && strings.Contains(post.Message, "Respond with a single valid JSON") {
			return true
		}
	}
	return false
}

// TestBridgeServiceCompletionStructuredOutputPolicy verifies a JSONOutputFormat
// request is resolved by the target service's structured-output policy: a
// known-capable auto combination keeps the native schema, everything else is
// converted into a prompt instruction, and an explicit policy overrides
// automatic detection.
func TestBridgeServiceCompletionStructuredOutputPolicy(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	withPolicy := func(svc llm.ServiceConfig, policy llm.StructuredOutputPolicy) llm.ServiceConfig {
		svc.StructuredOutputPolicy = policy
		return svc
	}

	tests := []struct {
		name       string
		services   []llm.ServiceConfig
		wantNative bool
	}{
		{
			name: "auto with a known-capable openai model sends the native schema",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAI, "gpt-4o"),
			},
			wantNative: true,
		},
		{
			name: "auto with another known-capable openai family sends the native schema",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAI, "gpt-4.1-mini"),
			},
			wantNative: true,
		},
		{
			name: "auto with an unrecognized openai model uses the prompt fallback",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAI, "mystery-model-1"),
			},
		},
		{
			name: "auto with an openai-compatible endpoint always uses the prompt fallback",
			services: []llm.ServiceConfig{
				bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAICompatible, "gpt-4o"),
			},
		},
		{
			name: "explicit native policy overrides an unknown combination",
			services: []llm.ServiceConfig{
				withPolicy(
					bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAICompatible, "gpt-4o"),
					llm.StructuredOutputPolicyNative,
				),
			},
			wantNative: true,
		},
		{
			name: "explicit prompt fallback policy overrides a capable combination",
			services: []llm.ServiceConfig{
				withPolicy(
					bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAI, "gpt-4o"),
					llm.StructuredOutputPolicyPromptFallback,
				),
			},
		},
		{
			name: "a fallback that cannot take a native schema puts the whole chain on the prompt fallback",
			services: []llm.ServiceConfig{
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAI, "gpt-4o")
					svc.FallbackServiceID = "svc-backup"
					return svc
				}(),
				bridgeServiceConfig("svc-backup", "Backup", llm.ServiceTypeOpenAICompatible, "gpt-4o"),
			},
		},
		{
			name: "a chain that is capable end to end keeps the native schema",
			services: []llm.ServiceConfig{
				func() llm.ServiceConfig {
					svc := bridgeServiceConfig("svc", "Service", llm.ServiceTypeOpenAI, "gpt-4o")
					svc.FallbackServiceID = "svc-backup"
					return svc
				}(),
				bridgeServiceConfig("svc-backup", "Backup", llm.ServiceTypeGemini, "gemini-2.5-pro"),
			},
			wantNative: true,
		},
	}

	for _, invoker := range serviceCompletionInvokers() {
		for _, tc := range tests {
			t.Run(invoker.name+"/"+tc.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.config.services = tc.services

				fake := NewFakeLLM(`{"answer":"42"}`)
				e.installServiceLLM(fake)

				client := e.CreateBridgeClient()
				_, err := invoker.call(client, "svc", bridgeclient.CompletionRequest{
					Posts:            []bridgeclient.Post{{Role: "user", Message: "Answer in JSON"}},
					JSONOutputFormat: bridgeJSONSchema,
				})
				require.NoError(t, err)

				request := fake.LastRequest()
				if tc.wantNative {
					require.NotNil(t, fake.LastConfig.JSONOutputFormat, "the native schema must reach the provider")
					require.False(t, systemPostContainsJSONInstruction(request), "no prompt instruction is needed on the native path")
					return
				}

				require.Nil(t, fake.LastConfig.JSONOutputFormat, "the schema must be stripped on the prompt fallback path")
				require.True(t, systemPostContainsJSONInstruction(request), "the prompt fallback must inject a JSON instruction")
			})
		}
	}
}

// TestBridgeServiceCompletionReleasesLease verifies the leased model is
// released exactly once per request, including when the call itself fails after
// acquisition.
func TestBridgeServiceCompletionReleasesLease(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name string
		fake *FakeLLM
	}{
		{
			name: "successful completion",
			fake: NewFakeLLM("done"),
		},
		{
			name: "failing completion",
			fake: NewFakeLLMWithError(fmt.Errorf("provider unavailable")),
		},
	}

	for _, invoker := range serviceCompletionInvokers() {
		for _, tc := range tests {
			t.Run(invoker.name+"/"+tc.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.config.services = []llm.ServiceConfig{
					bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
				}
				acquirer := e.installServiceLLM(tc.fake)

				client := e.CreateBridgeClient()
				_, _ = invoker.call(client, "svc-openai", bridgeclient.CompletionRequest{
					Posts: []bridgeclient.Post{{Role: "user", Message: "Hello"}},
				})

				require.Len(t, acquirer.acquireCalls(), 1)
				require.Equal(t, 1, acquirer.releaseCount())
			})
		}
	}
}

// TestBridgeServiceCompletionAcquisitionFailure verifies a failure to build the
// language model surfaces as a server error rather than a missing service, and
// that nothing is left leased.
func TestBridgeServiceCompletionAcquisitionFailure(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for _, invoker := range serviceCompletionInvokers() {
		t.Run(invoker.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.config.services = []llm.ServiceConfig{
				bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
			}
			acquirer := e.installServiceLLM(NewFakeLLM("unused"))
			acquirer.err = fmt.Errorf("provider client build failed")

			client := e.CreateBridgeClient()
			_, err := invoker.call(client, "svc-openai", bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{{Role: "user", Message: "Hello"}},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "provider client build failed")
			require.Equal(t, 0, acquirer.releaseCount())
		})
	}
}

// TestBridgeServiceCompletionRequestValidation verifies request validation on
// the service endpoints is unchanged, and that a rejected request never leases a
// language model.
func TestBridgeServiceCompletionRequestValidation(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		request     bridgeclient.CompletionRequest
		expectedErr string
	}{
		{
			name:        "empty posts",
			request:     bridgeclient.CompletionRequest{Posts: []bridgeclient.Post{}},
			expectedErr: "posts array cannot be empty",
		},
		{
			name: "invalid role",
			request: bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{{Role: "wizard", Message: "Hello"}},
			},
			expectedErr: "invalid role",
		},
		{
			name: "allowed tools",
			request: bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "Hello"}},
				AllowedTools: []string{"eligible_tool"},
			},
			expectedErr: "allowed_tools is only supported for agent completion endpoints",
		},
		{
			name: "invalid user id",
			request: bridgeclient.CompletionRequest{
				Posts:  []bridgeclient.Post{{Role: "user", Message: "Hello"}},
				UserID: "bad",
			},
			expectedErr: "invalid user_id",
		},
		{
			name: "invalid channel id",
			request: bridgeclient.CompletionRequest{
				Posts:     []bridgeclient.Post{{Role: "user", Message: "Hello"}},
				ChannelID: "bad",
			},
			expectedErr: "invalid channel_id",
		},
	}

	for _, invoker := range serviceCompletionInvokers() {
		for _, tc := range tests {
			t.Run(invoker.name+"/"+tc.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.config.services = []llm.ServiceConfig{
					bridgeServiceConfig("svc-openai", "OpenAI", llm.ServiceTypeOpenAI, "gpt-4o"),
				}
				acquirer := e.installServiceLLM(NewFakeLLM("unused"))

				client := e.CreateBridgeClient()
				_, err := invoker.call(client, "svc-openai", tc.request)
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
				require.Empty(t, acquirer.acquireCalls(), "a rejected request must not lease a model")
			})
		}
	}
}

// TestResolveBridgeService covers the resolution rules directly, including the
// cases the HTTP layer cannot reach (an empty path value is rejected by the
// handler before resolution runs).
func TestResolveBridgeService(t *testing.T) {
	services := []llm.ServiceConfig{
		bridgeServiceConfig("svc-b", "svc-a", llm.ServiceTypeAnthropic, "claude-sonnet-4-5"),
		bridgeServiceConfig("svc-a", "Shared", llm.ServiceTypeOpenAI, "gpt-4o"),
		bridgeServiceConfig("svc-c", "Shared", llm.ServiceTypeOpenAI, "gpt-4o"),
		bridgeServiceConfig("svc-blank", "", llm.ServiceTypeOpenAI, "gpt-4o"),
	}

	tests := []struct {
		name       string
		value      string
		expectedID string
		expectFind bool
	}{
		{name: "id match", value: "svc-c", expectedID: "svc-c", expectFind: true},
		{name: "id match beats an earlier name match", value: "svc-a", expectedID: "svc-a", expectFind: true},
		{name: "name match", value: "Shared", expectedID: "svc-a", expectFind: true},
		{name: "unknown value", value: "nope"},
		{name: "blank value never matches a blank name", value: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, found := resolveBridgeService(services, tc.value)
			require.Equal(t, tc.expectFind, found)
			require.Equal(t, tc.expectedID, svc.ID)
		})
	}
}

// TestServiceCanServeCompletionsGuardsDiscoveryAndCompletion pins the shared
// eligibility rule the discovery and completion paths both apply, so the two
// cannot drift apart.
func TestServiceCanServeCompletionsGuardsDiscoveryAndCompletion(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.config.services = []llm.ServiceConfig{
		bridgeServiceConfig("svc-ok", "OK", llm.ServiceTypeOpenAI, "gpt-4o"),
		bridgeServiceConfig("svc-scale", "Scale", llm.ServiceTypeScale, "scale-model"),
		bridgeServiceConfig("svc-modelless", "Modelless", llm.ServiceTypeOpenAI, ""),
	}
	e.installServiceLLM(NewFakeLLM("ok"))

	client := e.CreateBridgeClient()
	services, err := client.GetServices("")
	require.NoError(t, err)
	require.Equal(t, []string{"svc-ok"}, bridgeServiceIDs(services))

	for _, svc := range e.config.services {
		_, err := client.ServiceCompletion(svc.ID, bridgeclient.CompletionRequest{
			Posts: []bridgeclient.Post{{Role: "user", Message: "Hello"}},
		})
		if bots.ServiceCanServeCompletions(svc) {
			require.NoError(t, err, "discoverable service %q must be callable", svc.ID)
			continue
		}
		require.Error(t, err, "undiscoverable service %q must not be callable", svc.ID)
		require.Contains(t, err.Error(), "service not found: "+svc.ID)
	}
}
