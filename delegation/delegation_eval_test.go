// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callRecorder captures which tools the model actually called.
type callRecorder struct {
	mu    sync.Mutex
	calls map[string][]json.RawMessage
}

func (r *callRecorder) record(name string, args json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string][]json.RawMessage{}
	}
	r.calls[name] = append(r.calls[name], args)
}

func (r *callRecorder) called(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls[name]) > 0
}

func (r *callRecorder) firstArgs(name string) json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls[name]) == 0 {
		return nil
	}
	return r.calls[name][0]
}

type askAgentEvalArgs struct {
	Agent string `json:"agent" jsonschema:"Username (with or without a leading @) or bot user ID of the target agent"`
	Task  string `json:"task" jsonschema:"Self-contained task description for the target agent"`
}

type searchPostsEvalArgs struct {
	Term string `json:"term" jsonschema:"Search term"`
}

// buildEvalToolStore mirrors the delegating agent's tool surface: ask_agent,
// list_agents, and a representative read tool. Resolvers return canned data
// and record invocations.
func buildEvalToolStore(recorder *callRecorder) *llm.ToolStore {
	store := llm.NewToolStore()
	record := func(name string, next func(args json.RawMessage) string) llm.ToolResolver {
		return func(_ context.Context, _ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			var raw json.RawMessage
			if err := argsGetter(&raw); err != nil {
				return "", err
			}
			recorder.record(name, raw)
			return next(raw), nil
		}
	}

	store.AddTools([]llm.Tool{
		{
			Name:         "mattermost__ask_agent",
			Description:  "Delegate a task to an AI agent on behalf of the user and return its answer. The target may be your own agent identity when a separate delegated turn is useful. The task must be fully self-contained. Use list_agents to discover agents.",
			Schema:       llm.NewJSONSchemaFromStruct[askAgentEvalArgs](),
			ServerOrigin: mcp.EmbeddedClientKey,
			Resolver: record("mattermost__ask_agent", func(json.RawMessage) string {
				return format.DelegationResult("Projects Agent", "projects", "https://example.com/_redirect/pl/abc", "Last sprint the Icarus project shipped the realtime sync engine and the new billing dashboard.")
			}),
		},
		{
			Name:         "mattermost__list_agents",
			Description:  "List all available AI agents (bots). Returns each agent's ID, display name, and username.",
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			ServerOrigin: mcp.EmbeddedClientKey,
			Resolver: record("mattermost__list_agents", func(json.RawMessage) string {
				return format.AgentList([]format.AgentInfo{
					{ID: "matty-bot-id", DisplayName: "Matty", Username: "matty"},
					{ID: "projects-bot-id", DisplayName: "Projects Agent (expert on the Icarus project status)", Username: "projects"},
				}, "matty-bot-id")
			}),
		},
		{
			Name:         "mattermost__search_posts",
			Description:  "Search Mattermost posts by term.",
			Schema:       llm.NewJSONSchemaFromStruct[searchPostsEvalArgs](),
			ServerOrigin: mcp.EmbeddedClientKey,
			Resolver: record("mattermost__search_posts", func(json.RawMessage) string {
				return "No results found."
			}),
		},
	})
	return store
}

// TestDelegationRoutingEval checks that an agent with ask_agent available
// delegates when another agent is clearly better suited, and answers directly
// when it is not.
func TestDelegationRoutingEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	tests := []struct {
		name             string
		message          string
		wantDelegation   bool
		wantTaskContains string
		rubrics          []string
	}{
		{
			name:             "explicit delegation request",
			message:          "Ask the projects agent what shipped last sprint in the Icarus project.",
			wantDelegation:   true,
			wantTaskContains: "Icarus",
			rubrics: []string{
				"mentions the realtime sync engine and the billing dashboard as having shipped",
			},
		},
		{
			name:             "task in another agent's domain",
			message:          "I need the current Icarus project status. The projects agent tracks that.",
			wantDelegation:   true,
			wantTaskContains: "Icarus",
			rubrics: []string{
				"states what shipped in the Icarus project (the realtime sync engine and/or the billing dashboard)",
			},
		},
		{
			name:           "simple question is answered directly",
			message:        "What is 15% of 240?",
			wantDelegation: false,
			rubrics: []string{
				"answers 36 without mentioning delegating to another agent",
			},
		},
		{
			name:           "self-contained writing task is answered directly",
			message:        "Rewrite this sentence to be more formal: 'hey team, the thing is done'",
			wantDelegation: false,
			rubrics: []string{
				"contains a rewritten version of the sentence in a more formal register (any reasonable phrasing counts, e.g. announcing that the task or work is complete)",
			},
		},
	}

	for _, tc := range tests {
		evals.Run(t, "delegation routing "+tc.name, func(e *evals.EvalT) {
			recorder := &callRecorder{}

			llmContext := llm.NewContext()
			llmContext.SetBotFields("Matty", "matty", "matty-bot-id", "mattermodel", "anthropic", "")
			llmContext.ServerName = "Eval Server"
			llmContext.RequestingUser = &model.User{Id: "user-1", Username: "alice"}
			llmContext.Channel = &model.Channel{Id: "channel-1", Type: model.ChannelTypeDirect, Name: "alice__matty"}
			llmContext.Tools = buildEvalToolStore(recorder)

			systemPrompt, err := e.Prompts.Format(prompts.PromptDirectMessageQuestionSystem, llmContext)
			require.NoError(e.T, err)

			runner := toolrunner.New(e.LLM)
			runResult, err := runner.Run(context.Background(), llm.CompletionRequest{
				Posts: []llm.Post{
					{Role: llm.PostRoleSystem, Message: systemPrompt},
					{Role: llm.PostRoleUser, Message: tc.message},
				},
				Context:   llmContext,
				Operation: llm.OperationConversation,
			}, func(llm.ToolCall) bool { return true }, nil)
			require.NoError(e.T, err)

			response, err := runResult.Stream.ReadAll()
			require.NoError(e.T, err)
			e.Logf("LLM response:\n%s", response)

			if tc.wantDelegation {
				require.True(e.T, recorder.called("mattermost__ask_agent"), "expected the model to call ask_agent")
				var args askAgentEvalArgs
				require.NoError(e.T, json.Unmarshal(recorder.firstArgs("mattermost__ask_agent"), &args))
				assert.Contains(e.T, args.Agent, "projects", "delegation should target the projects agent")
				assert.NotEmpty(e.T, args.Task, "delegation task must not be empty")
				assert.Contains(e.T, args.Task, tc.wantTaskContains, "the delegated task must preserve the user's actual request")
			} else {
				assert.False(e.T, recorder.called("mattermost__ask_agent"), "the model should answer directly without delegating")
			}

			for _, rubric := range tc.rubrics {
				evals.LLMRubricT(e, rubric, response)
			}
		})
	}
}
