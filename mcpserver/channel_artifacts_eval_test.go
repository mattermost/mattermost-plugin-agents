// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	listArtifactsTool   = "list_channel_artifacts"
	getArtifactTool     = "get_channel_artifact"
	saveArtifactTool    = mmtools.SaveChannelArtifactToolName
	removeArtifactTool  = mmtools.RemoveChannelArtifactToolName
	createFileToolName  = mmtools.CreateFileToolName
	runbookArtifactName = "Migration cutover runbook"
	checklistName       = "Launch checklist"
)

var artifactToolNames = []string{listArtifactsTool, getArtifactTool, saveArtifactTool, removeArtifactTool}

const runbookHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Migration cutover runbook</title>
<style>body{font-family:sans-serif;max-width:640px;margin:2rem auto}td,th{border:1px solid #ccc;padding:4px 8px}</style></head>
<body>
<h1>Migration cutover runbook</h1>
<p>MySQL to PostgreSQL cutover for the billing database.</p>
<table>
<tr><th>Item</th><th>Detail</th></tr>
<tr><td>Cutover window</td><td>Thursday 22:00-23:30 UTC</td></tr>
<tr><td>Rollback owner</td><td>Priya Raman</td></tr>
<tr><td>Go/no-go deadline</td><td>Thursday 21:15 UTC</td></tr>
<tr><td>Replication lag limit</td><td>5 seconds</td></tr>
</table>
<h2>Steps</h2>
<ol><li>Put the app in read-only mode.</li><li>Wait for replication lag to reach zero.</li><li>Switch the connection string.</li><li>Run smoke tests.</li></ol>
</body></html>`

const checklistHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Launch checklist</title></head>
<body>
<h1>Launch checklist</h1>
<ul>
<li>Freeze the deploy pipeline at 17:00 Friday</li>
<li>Notify support about the maintenance window</li>
<li>Rotate the staging TLS certificate</li>
<li>Snapshot the primary database</li>
<li>Update the public status page after traffic moves</li>
</ul>
</body></html>`

// fakeChannelArtifacts stands in for the plugin-hosted library, which the eval
// container does not run. Entries are seeded per channel.
type fakeChannelArtifacts struct {
	mu      sync.Mutex
	entries map[string]*channelartifacts.Entry
	content map[string]string
	order   []string
}

func newFakeChannelArtifacts() *fakeChannelArtifacts {
	return &fakeChannelArtifacts{entries: map[string]*channelartifacts.Entry{}, content: map[string]string{}}
}

func (f *fakeChannelArtifacts) add(channel *model.Channel, title, html string) *channelartifacts.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := &channelartifacts.Entry{
		ID:                     model.NewId(),
		ChannelID:              channel.Id,
		Title:                  title,
		Version:                1,
		SHA256:                 "9f2b7c1d4e8a0b3c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4",
		Size:                   int64(len(html)),
		SourcePostID:           model.NewId(),
		SourceFileID:           model.NewId(),
		SourceAgentDisplayName: "Matty",
		CreateAt:               model.GetMillis(),
		UpdateAt:               model.GetMillis(),
		Available:              true,
	}
	f.entries[entry.ID] = entry
	f.content[entry.ID] = html
	f.order = append(f.order, entry.ID)
	return entry
}

func (f *fakeChannelArtifacts) Enabled() bool { return true }

func (f *fakeChannelArtifacts) List(_ context.Context, _, channelID string) ([]*channelartifacts.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*channelartifacts.Entry
	for _, id := range f.order {
		if f.entries[id].ChannelID == channelID {
			out = append(out, f.entries[id])
		}
	}
	return out, nil
}

func (f *fakeChannelArtifacts) Get(_ context.Context, _, artifactID string, opts tools.ChannelArtifactReadOptions) (tools.ChannelArtifactContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[artifactID]
	if !ok {
		return tools.ChannelArtifactContent{}, channelartifacts.ErrNotFound
	}
	view := tools.ChannelArtifactContent{}
	if opts.IncludeContent {
		view = tools.ChannelArtifactWindow(f.content[artifactID], opts.Offset, opts.Limit)
	}
	view.Entry = entry
	return view, nil
}

// libraryWrite is one SaveChannelArtifact or RemoveChannelArtifact call made
// by the model under test.
type libraryWrite struct {
	tool       string
	artifactID string
	title      string
	content    string
	fileID     string
}

// createdFile is one CreateFile call made by the model under test.
type createdFile struct {
	name    string
	content string
}

// artifactEvalEnv is the seeded world the artifact evals run in.
type artifactEvalEnv struct {
	team      *model.Team
	channel   *model.Channel
	requester *model.User
	library   *fakeChannelArtifacts
	runbook   *channelartifacts.Entry
	checklist *channelartifacts.Entry
}

func seedArtifactEnv(t *testing.T, suite *TestSuite) *artifactEvalEnv {
	t.Helper()
	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	library := newFakeChannelArtifacts()
	return &artifactEvalEnv{
		team:      data.team,
		channel:   data.channel,
		requester: data.alice,
		library:   library,
		runbook:   library.add(data.channel, runbookArtifactName, runbookHTML),
		checklist: library.add(data.channel, checklistName, checklistHTML),
	}
}

// newRecordingCreateFileTool keeps the production CreateFile description and
// schema so the model sees what it sees in the plugin, but records files
// instead of uploading them.
func newRecordingCreateFileTool(mu *sync.Mutex, files *[]createdFile) llm.Tool {
	tool := mmtools.NewCreateFileTool(nil, true)
	tool.Resolver = func(_ context.Context, _ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args mmtools.CreateFileArgs
		if err := argsGetter(&args); err != nil {
			return "invalid parameters to function", err
		}
		mu.Lock()
		*files = append(*files, createdFile{name: args.FileName, content: args.Content})
		mu.Unlock()
		out, err := json.Marshal(mmtools.CreateFileResult{
			FileID:   model.NewId(),
			FileName: args.FileName,
			Note:     "Attached to your reply automatically — do not repeat the file's content in your response text.",
		})
		return string(out), err
	}
	return tool
}

// newRecordingLibraryTools keeps the production SaveChannelArtifact and
// RemoveChannelArtifact descriptions and schemas, but records the calls and
// answers like the plugin does for the seeded library.
func newRecordingLibraryTools(llmCtx *llm.Context, env *artifactEvalEnv, mu *sync.Mutex, writes *[]libraryWrite) []llm.Tool {
	production := (&mmtools.ChannelArtifactTools{
		Library: channelartifacts.NewLibrary(&channelartifacts.Service{}, func() channelartifacts.Limits { return channelartifacts.Limits{} }),
		Enabled: func() bool { return true },
	}).Tools(llmCtx)

	record := func(w libraryWrite) {
		mu.Lock()
		*writes = append(*writes, w)
		mu.Unlock()
	}
	marshal := func(r mmtools.ChannelArtifactResult) (string, error) {
		out, err := json.Marshal(r)
		return string(out), err
	}

	out := make([]llm.Tool, 0, len(production))
	for _, tool := range production {
		switch tool.Name {
		case saveArtifactTool:
			tool.Resolver = func(_ context.Context, _ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				var args mmtools.SaveChannelArtifactArgs
				if err := argsGetter(&args); err != nil {
					return "invalid parameters to function", err
				}
				record(libraryWrite{tool: saveArtifactTool, artifactID: args.ArtifactID, title: args.Title, content: args.Content, fileID: args.FileID})
				if (args.Content == "") == (args.FileID == "") {
					return "provide exactly one of content or file_id", errors.New("needs content or file_id")
				}
				if args.ArtifactID == "" && strings.TrimSpace(args.Title) == "" && args.Content != "" {
					return "title is required for a new artifact", errors.New("missing title")
				}
				if args.ArtifactID != "" {
					entry, ok := env.library.entries[args.ArtifactID]
					if !ok || entry.ChannelID != env.channel.Id {
						return "the requesting user may not manage channel artifacts here, or the artifact was not found in this channel", errors.New("artifact not in this channel")
					}
					return marshal(mmtools.ChannelArtifactResult{ArtifactID: entry.ID, Title: entry.Title, Version: entry.Version + 1, PostID: model.NewId(),
						Note: "Saved. The library keeps a reference to the message with the file, so the artifact lasts only as long as that message does."})
				}
				return marshal(mmtools.ChannelArtifactResult{ArtifactID: model.NewId(), Title: args.Title, Version: 1, PostID: model.NewId(),
					Note: "Saved. The library keeps a reference to the message with the file, so the artifact lasts only as long as that message does."})
			}
		case removeArtifactTool:
			tool.Resolver = func(_ context.Context, _ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				var args mmtools.RemoveChannelArtifactArgs
				if err := argsGetter(&args); err != nil {
					return "invalid parameters to function", err
				}
				record(libraryWrite{tool: removeArtifactTool, artifactID: args.ArtifactID})
				if entry, ok := env.library.entries[args.ArtifactID]; !ok || entry.ChannelID != env.channel.Id {
					return "the requesting user may not manage channel artifacts here, or the artifact was not found in this channel", errors.New("artifact not in this channel")
				}
				return marshal(mmtools.ChannelArtifactResult{ArtifactID: args.ArtifactID, Note: "Removed from the library. The original message and file were not deleted."})
			}
		}
		out = append(out, tool)
	}
	return out
}

type artifactEvalCase struct {
	name    string
	message string
	// required lists tools the model must call.
	required []string
	// forbidden lists tools the model must not call.
	forbidden []string
	rubrics   []string
	check     func(e *evals.EvalT, env *artifactEvalEnv, run artifactRun)
}

// artifactRun is what the model under test did in one trial.
type artifactRun struct {
	files  []createdFile
	writes []libraryWrite
}

// runArtifactEval runs one conversation turn through the production system
// prompt and the real MCP tool definitions, with CreateFile beside them.
func runArtifactEval(e *evals.EvalT, suite *TestSuite, env *artifactEvalEnv, c artifactEvalCase) {
	e.Helper()

	// Libraries exist only in public and private channels, and the plugin never
	// offers the artifact tools in a direct or group message. Run in the seeded
	// channel, not the shared helper's default direct message.
	require.True(e, channelartifacts.SupportsChannelType(env.channel.Type), "artifact evals must run in a public or private channel")
	setup := setupAgenticEval(e, suite, env.requester, env.team)
	var (
		filesMu sync.Mutex
		files   []createdFile
	)
	var writes []libraryWrite
	setup.llmContext.Channel = env.channel
	setup.llmContext.BotUserID = model.NewId()
	setup.llmContext.ToolCatalog.ResponseFilesSupported = true
	setup.llmContext.Tools.AddTools([]llm.Tool{newRecordingCreateFileTool(&filesMu, &files)})
	setup.llmContext.Tools.AddTools(newRecordingLibraryTools(setup.llmContext, env, &filesMu, &writes))

	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(e, err, "Failed to load prompts")
	systemPrompt, err := promptsObj.Format(prompts.PromptDirectMessageQuestionSystem, setup.llmContext)
	require.NoError(e, err, "Failed to format system prompt")

	runner := toolrunner.New(setup.llm)
	runResult, err := runner.Run(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: systemPrompt},
			{Role: llm.PostRoleUser, Message: c.message},
		},
		Context:   setup.llmContext,
		Operation: llm.OperationConversation,
	}, func(llm.ToolCall) bool { return true }, nil)
	require.NoError(e, err, "ToolRunner should succeed")

	response, err := runResult.Stream.ReadAll()
	require.NoError(e, err, "ReadAll should succeed")
	e.Logf("LLM response:\n%s", response)

	called := setup.logger.CalledTools()
	for _, name := range c.required {
		assert.True(e, slices.Contains(called, name), "%s was not called (called tools: %v)", name, called)
	}
	for _, name := range c.forbidden {
		assert.False(e, slices.Contains(called, name), "%s must not be called (called tools: %v)", name, called)
	}

	filesMu.Lock()
	run := artifactRun{files: slices.Clone(files), writes: slices.Clone(writes)}
	filesMu.Unlock()
	if c.check != nil {
		c.check(e, env, run)
	}
	for _, rubric := range c.rubrics {
		evals.LLMRubricT(e, rubric, response)
	}
}

func hasHTMLFile(files []createdFile) bool {
	return slices.ContainsFunc(files, func(f createdFile) bool {
		return strings.HasSuffix(strings.ToLower(f.name), ".html")
	})
}

// TestChannelArtifactAdoptionEval checks that models reach for the channel
// library tools when the request calls for them without being told which
// tool to use, and leave them alone otherwise.
func TestChannelArtifactAdoptionEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()

	env := seedArtifactEnv(t, suite)
	suite.CreateMCPServerWithChannelArtifacts(false, env.library)

	// anyFileContaining reports whether the model produced HTML, as an
	// attached file or a library save, that contains every fragment.
	anyHTMLContaining := func(run artifactRun, fragments ...string) bool {
		has := func(content string) bool {
			return !slices.ContainsFunc(fragments, func(f string) bool { return !strings.Contains(content, f) })
		}
		return slices.ContainsFunc(run.files, func(f createdFile) bool { return has(f.content) }) ||
			slices.ContainsFunc(run.writes, func(w libraryWrite) bool { return has(w.content) })
	}
	savesOf := func(run artifactRun) []libraryWrite {
		return slices.DeleteFunc(slices.Clone(run.writes), func(w libraryWrite) bool { return w.tool != saveArtifactTool })
	}

	cases := []artifactEvalCase{
		{
			name:     "discovers an existing saved page",
			message:  "Before I build anything new for the cutover: has anyone already put together a runbook or checklist page for this channel?",
			required: []string{listArtifactsTool},
			rubrics:  []string{"Mentions that the channel already has a migration cutover runbook and a launch checklist"},
		},
		{
			name:     "answers from a saved page",
			message:  "Who owns rollback for the cutover, and when is the go/no-go deadline? It's on the runbook page we put together.",
			required: []string{listArtifactsTool, getArtifactTool},
			rubrics: []string{
				"States that Priya Raman owns rollback",
				"States the go/no-go deadline is Thursday 21:15 UTC",
			},
		},
		{
			name:     "answers from a saved page without being pointed to it",
			message:  "What's the replication lag limit for the cutover?",
			required: []string{getArtifactTool},
			rubrics:  []string{"States the replication lag limit is 5 seconds"},
		},
		{
			name:     "looks for a checklist without being pointed to the library",
			message:  "Do we have a launch checklist anywhere for this channel? If so, what's on it?",
			required: []string{listArtifactsTool},
			rubrics:  []string{"Lists items from the launch checklist such as freezing the deploy pipeline or rotating the staging TLS certificate"},
		},
		{
			name:      "reviews a saved page without changing it",
			message:   "Read through the cutover runbook page we have and tell me what steps look like they're missing.",
			required:  []string{getArtifactTool},
			forbidden: []string{saveArtifactTool, removeArtifactTool},
		},
		{
			name:     "builds on a saved page",
			message:  "Take our launch checklist page and turn it into a one-page printable HTML status board for the channel.",
			required: []string{getArtifactTool},
			check: func(e *evals.EvalT, _ *artifactEvalEnv, run artifactRun) {
				assert.True(e, anyHTMLContaining(run, "TLS certificate"), "the new page should carry the saved checklist items")
			},
		},
		{
			name:     "updates a saved page when asked to save the change",
			message:  "Add a row for 'Comms lead: Jordan Lee' to the cutover runbook page we have saved, and save the change so everyone sees it.",
			required: []string{getArtifactTool, saveArtifactTool},
			check: func(e *evals.EvalT, env *artifactEvalEnv, run artifactRun) {
				saves := savesOf(run)
				require.NotEmpty(e, saves)
				last := saves[len(saves)-1]
				assert.Equal(e, env.runbook.ID, last.artifactID, "the change should be a new version of the saved runbook, not a new artifact")
				assert.True(e, anyHTMLContaining(run, "Jordan Lee", "Priya Raman", "21:15 UTC"), "the saved HTML should add Jordan Lee while keeping the saved runbook content")
			},
		},
		{
			name:     "updates a saved page without being told to save",
			message:  "Add a row for 'Comms lead: Jordan Lee' to the cutover runbook page.",
			required: []string{getArtifactTool},
			check: func(e *evals.EvalT, _ *artifactEvalEnv, run artifactRun) {
				assert.True(e, anyHTMLContaining(run, "Jordan Lee", "Priya Raman", "21:15 UTC"), "the revised page should add Jordan Lee while keeping the saved runbook content")
			},
		},
		{
			name:     "saves a new page when asked",
			message:  "Make a small HTML countdown page for Thursday's 22:00 UTC cutover and save it to this channel's artifacts so the team can find it.",
			required: []string{saveArtifactTool},
			check: func(e *evals.EvalT, _ *artifactEvalEnv, run artifactRun) {
				saves := savesOf(run)
				require.NotEmpty(e, saves)
				assert.Empty(e, saves[0].artifactID, "a new page should not overwrite an existing artifact")
				assert.NotEmpty(e, saves[0].title, "a new artifact needs a title")
				assert.True(e, anyHTMLContaining(run, "<html"), "the saved content should be HTML")
			},
			rubrics: []string{"Says the page was saved to the channel's artifacts"},
		},
		{
			name:      "makes a page without saving it when not asked",
			message:   "Make a small HTML countdown page for Thursday's 22:00 UTC cutover.",
			required:  []string{createFileToolName},
			forbidden: []string{saveArtifactTool, removeArtifactTool},
			check: func(e *evals.EvalT, _ *artifactEvalEnv, run artifactRun) {
				assert.True(e, hasHTMLFile(run.files), "an HTML file should be created")
			},
		},
		{
			name:     "removes a saved page when asked",
			message:  "We don't use the launch checklist page anymore. Take it out of the channel's saved pages.",
			required: []string{removeArtifactTool},
			check: func(e *evals.EvalT, env *artifactEvalEnv, run artifactRun) {
				for _, w := range run.writes {
					if w.tool == removeArtifactTool {
						assert.Equal(e, env.checklist.ID, w.artifactID, "the launch checklist, not the runbook, should be removed")
					}
				}
				assert.Empty(e, savesOf(run), "nothing should be saved when asked to remove")
			},
		},

		{
			name:      "negative: summarizes the channel",
			message:   "Give me a quick summary of what's been discussed in this channel.",
			required:  []string{"read_channel"},
			forbidden: artifactToolNames,
		},
		{
			name:      "negative: writes a markdown file",
			message:   "Write up the migration risks we discussed as a markdown document I can download.",
			required:  []string{createFileToolName},
			forbidden: artifactToolNames,
			check: func(e *evals.EvalT, _ *artifactEvalEnv, run artifactRun) {
				assert.False(e, hasHTMLFile(run.files), "a markdown request should not produce an HTML file")
			},
		},
		{
			name:      "negative: searches posts",
			message:   "Find what Charlie said about the data volume.",
			forbidden: artifactToolNames,
			rubrics:   []string{"Mentions about 500GB of data in the MySQL instance or the downtime concern"},
		},
		{
			name:      "negative: general question",
			message:   "What's the difference between a clustered and a non-clustered index?",
			forbidden: append([]string{createFileToolName}, artifactToolNames...),
			rubrics:   []string{"Explains that a clustered index determines the physical order of rows while a non-clustered index is a separate structure pointing to the rows"},
		},
	}

	for _, c := range cases {
		evals.Run(t, "channel artifacts "+c.name, func(e *evals.EvalT) {
			runArtifactEval(e, suite, env, c)
		})
	}
}
