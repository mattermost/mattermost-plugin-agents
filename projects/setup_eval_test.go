// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// setupCall is one tool call the setup turn made, with the item titles it
// put on a board or checklist.
type setupCall struct {
	name  string
	items []string
}

type setupRecorder struct {
	mu    sync.Mutex
	calls []setupCall
}

func (r *setupRecorder) record(name string, items ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, setupCall{name: name, items: items})
}

func (r *setupRecorder) called(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.calls, func(c setupCall) bool { return c.name == name })
}

// itemsOf returns the titles every call in names put on the board or the
// checklist.
func (r *setupRecorder) itemsOf(names ...string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var items []string
	for _, c := range r.calls {
		if slices.Contains(names, c.name) {
			items = append(items, c.items...)
		}
	}
	return items
}

func (r *setupRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, c := range r.calls {
		names = append(names, c.name)
	}
	return names
}

type titled struct {
	Title string `json:"title"`
}

type checklistArgs struct {
	Name     string `json:"name"`
	Sections []struct {
		Title string   `json:"title"`
		Items []titled `json:"items"`
	} `json:"sections"`
}

func titlesOf(items []titled) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Title)
	}
	return out
}

// setupEvalTools are the tools a project setup turn has, as recording fakes
// with the names and shapes of the real Boards, Playbooks, artifact and
// setup tools.
func setupEvalTools(rec *setupRecorder) []llm.Tool {
	tool := func(name, description string, schema any, run func(raw json.RawMessage) string) llm.Tool {
		return llm.Tool{
			Name: name, Description: description, Schema: schema,
			Resolver: func(_ context.Context, _ *llm.Context, get llm.ToolArgumentGetter) (string, error) {
				var raw json.RawMessage
				if err := get(&raw); err != nil {
					return "", err
				}
				return run(raw), nil
			},
		}
	}
	plain := func(name string) func(json.RawMessage) string {
		return func(json.RawMessage) string {
			rec.record(name)
			return "Done."
		}
	}
	type cardArgs struct {
		Title string `json:"title" jsonschema:"The card's title."`
	}
	type boardArgs struct {
		Title string `json:"title" jsonschema:"The board's title."`
	}
	type itemArgs struct {
		Title string `json:"title" jsonschema:"The item's title."`
	}
	type artifactArgs struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	type charterArgs struct {
		ArtifactID string `json:"artifact_id"`
	}
	type noteArgs struct {
		Note string `json:"note"`
	}
	type nameArgs struct {
		DisplayName string `json:"display_name"`
	}
	type instructionsArgs struct {
		Instructions string `json:"instructions"`
	}
	type noArgs struct{}

	return []llm.Tool{
		tool("create_board", "Create a kanban board for the project, with To Do, In Progress and Done columns. Add cards with create_card.", llm.NewJSONSchemaFromStruct[boardArgs](), func(json.RawMessage) string {
			rec.record("create_board")
			return `{"board":{"channel_id":"BOARD"}}`
		}),
		tool("create_card", "Add a card to the project board.", llm.NewJSONSchemaFromStruct[cardArgs](), func(raw json.RawMessage) string {
			var args cardArgs
			_ = json.Unmarshal(raw, &args)
			rec.record("create_card", args.Title)
			return `{"card_id":"card"}`
		}),
		tool("create_checklist", "Create a checklist for the project channel, with sections of items people check off.", llm.NewJSONSchemaFromStruct[checklistArgs](), func(raw json.RawMessage) string {
			var args checklistArgs
			_ = json.Unmarshal(raw, &args)
			var items []string
			for _, section := range args.Sections {
				items = append(items, titlesOf(section.Items)...)
			}
			rec.record("create_checklist", items...)
			return `{"run_id":"RUN"}`
		}),
		tool("add_checklist_item", "Add an item to the project checklist.", llm.NewJSONSchemaFromStruct[itemArgs](), func(raw json.RawMessage) string {
			var args itemArgs
			_ = json.Unmarshal(raw, &args)
			rec.record("add_checklist_item", args.Title)
			return "Item added."
		}),
		tool(mmtools.SaveChannelArtifactToolName, "Save a Markdown document to the channel's artifact library.", llm.NewJSONSchemaFromStruct[artifactArgs](), func(json.RawMessage) string {
			rec.record(mmtools.SaveChannelArtifactToolName)
			return `{"artifact_id":"charter1"}`
		}),
		tool(ToolLinkCharter, "Record an artifact saved in the project channel's artifact library as the project charter.", llm.NewJSONSchemaFromStruct[charterArgs](), plain(ToolLinkCharter)),
		tool(ToolUpdateStatusNote, "Replace the project's status note with one or two sentences on where the project stands.", llm.NewJSONSchemaFromStruct[noteArgs](), plain(ToolUpdateStatusNote)),
		tool(agentsetup.ToolGetMyConfig, "Read your own current configuration.", llm.NewJSONSchemaFromStruct[noArgs](), func(json.RawMessage) string {
			return `{"display_name":"Launch Plan Agent","username":"launch-plan-agent","instructions":""}`
		}),
		tool(agentsetup.ToolSetMyDisplayName, "Change the display name people see for you.", llm.NewJSONSchemaFromStruct[nameArgs](), plain(agentsetup.ToolSetMyDisplayName)),
		tool(agentsetup.ToolUpdateMyInstructions, "Replace your own instructions.", llm.NewJSONSchemaFromStruct[instructionsArgs](), plain(agentsetup.ToolUpdateMyInstructions)),
		tool(agentsetup.ToolCompleteSetup, "Mark your setup as complete.", llm.NewJSONSchemaFromStruct[noArgs](), plain(agentsetup.ToolCompleteSetup)),
	}
}

type setupEvalCase struct {
	name          string
	purpose       string
	wantBoard     bool
	wantChecklist bool
	wantQuestion  bool
	rubrics       []string
}

func runSetupEval(e *evals.EvalT, c setupEvalCase) {
	e.Helper()
	rec := &setupRecorder{}
	llmCtx := llm.NewContext()
	llmCtx.BotName = "Launch Plan Agent"
	llmCtx.BotUsername = "launch-plan-agent"
	llmCtx.RequestingUser = &model.User{Id: model.NewId(), Username: "alice"}
	llmCtx.Channel = &model.Channel{Id: model.NewId(), Type: model.ChannelTypeOpen, Name: "launch-plan", DisplayName: "Launch Plan"}
	llmCtx.Tools = llm.NewToolStore()
	llmCtx.Tools.AddTools(setupEvalTools(rec))

	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(e, err)
	systemPrompt, err := promptsObj.Format(prompts.PromptProjectSetupSystem, llmCtx)
	require.NoError(e, err)
	project := &store.Project{ChannelID: llmCtx.Channel.Id, State: store.ProjectStateActive, LeadUserID: llmCtx.RequestingUser.Id}
	systemPrompt += "\n\n" + FormatStatusSuffix(project, false)

	result, err := toolrunner.New(e.LLM).Run(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: systemPrompt},
			{Role: llm.PostRoleUser, Message: setupKickoffInstruction(c.purpose)},
		},
		Context:   llmCtx,
		Operation: llm.OperationAgentSetup,
	}, func(llm.ToolCall) bool { return true }, nil)
	require.NoError(e, err)
	response, err := result.Stream.ReadAll()
	require.NoError(e, err)
	e.Logf("tools: %v\nresponse:\n%s", rec.names(), response)

	madeBoard, madeChecklist := rec.called("create_board"), rec.called("create_checklist")
	if c.wantQuestion {
		assert.False(e, madeBoard || madeChecklist, "a vague purpose gets a question, not a tracker")
	} else {
		assert.Equal(e, c.wantBoard, madeBoard, "board")
		assert.Equal(e, c.wantChecklist, madeChecklist, "checklist")
		assert.True(e, rec.called(mmtools.SaveChannelArtifactToolName), "the charter is saved")
	}
	assert.False(e, rec.called(agentsetup.ToolSetMyDisplayName), "nobody asked for another name")
	assert.False(e, rec.called(agentsetup.ToolUpdateMyInstructions), "nobody said how they want to work")

	if madeBoard && madeChecklist {
		board := rec.itemsOf("create_card")
		checklist := rec.itemsOf("create_checklist", "add_checklist_item")
		evals.LLMRubricT(e, "No item on the board is the same piece of work as an item on the checklist; reworded duplicates count as the same",
			fmt.Sprintf("Board cards:\n- %s\n\nChecklist items:\n- %s", strings.Join(board, "\n- "), strings.Join(checklist, "\n- ")))
	}
	for _, rubric := range c.rubrics {
		evals.LLMRubricT(e, rubric, response)
	}
}

func TestProjectSetupPicksTheTrackerEval(t *testing.T) {
	cases := []setupEvalCase{
		{
			name:          "a launch runbook gets a checklist",
			purpose:       "Launch the new pricing page on March 3. Before launch we need legal sign-off, translations for 4 locales, a load test, the marketing email scheduled, and then the DNS switch on the day. Done when the page is live and the email went out.",
			wantChecklist: true,
			rubrics:       []string{"Says in a sentence that it chose a checklist and gives a reason, such as the work being an ordered set of launch steps"},
		},
		{
			name:      "parallel workstreams get a board",
			purpose:   "Rebuild the mobile checkout. Four engineers work in parallel on the cart, payments (Apple Pay and Google Pay), address entry and the order confirmation screen, each moving from design to build to QA. Done when conversion on mobile is up 15%.",
			wantBoard: true,
			rubrics:   []string{"Says in a sentence that it chose a board and gives a reason, such as parallel pieces of work moving through stages"},
		},
		{
			name:          "workstreams with launch gates get both, without overlap",
			purpose:       "Ship the self-serve billing portal. Three teams build invoices, payment methods and plan changes in parallel, each moving from build to review to done. Separately, the launch has fixed gates in order: security review, finance sign-off, support training, then general availability on June 1.",
			wantBoard:     true,
			wantChecklist: true,
			rubrics:       []string{"Explains briefly that the board holds the parallel build work and the checklist holds the launch gates"},
		},
		{
			name:         "a vague purpose gets one question",
			purpose:      "Improve things for customers.",
			wantQuestion: true,
			rubrics:      []string{"Asks the team one short question about what the project should achieve or what done looks like, and asks at most one question"},
		},
	}
	for _, c := range cases {
		evals.Run(t, "project setup "+c.name, func(e *evals.EvalT) {
			runSetupEval(e, c)
		})
	}
}
