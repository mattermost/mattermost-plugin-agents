// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
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
	proposeUpdateTool   = "propose_channel_artifact_update"
	createFileToolName  = mmtools.CreateFileToolName
	runbookArtifactName = "Migration cutover runbook"
	checklistName       = "Launch checklist"
)

var artifactToolNames = []string{listArtifactsTool, getArtifactTool, proposeUpdateTool}

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
		ID:        model.NewId(),
		ChannelID: channel.Id,
		TeamID:    channel.TeamId,
		CreateAt:  model.GetMillis(),
		VersionSnapshot: channelartifacts.VersionSnapshot{
			Title:                  title,
			SourcePostID:           model.NewId(),
			SourceFileID:           model.NewId(),
			SourceFileName:         strings.ToLower(strings.ReplaceAll(title, " ", "-")) + ".html",
			SourceAgentDisplayName: "Matty",
			UpdateAt:               model.GetMillis(),
			Size:                   int64(len(html)),
			SHA256:                 "9f2b7c1d4e8a0b3c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4",
			Version:                1,
		},
	}
	f.entries[entry.ID] = entry
	f.content[entry.ID] = html
	f.order = append(f.order, entry.ID)
	return entry
}

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

func (f *fakeChannelArtifacts) Get(_ context.Context, _, artifactID string, includeContent bool, contentLimit int) (tools.ChannelArtifactContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[artifactID]
	if !ok {
		return tools.ChannelArtifactContent{}, channelartifacts.ErrNotFound
	}
	view := tools.ChannelArtifactContent{Entry: entry, ContentIncluded: includeContent}
	if includeContent {
		body := f.content[artifactID]
		view.TotalRunes = utf8.RuneCountInString(body)
		runes := []rune(body)
		if contentLimit > 0 && len(runes) > contentLimit {
			runes = runes[:contentLimit]
			view.HasMore = true
		}
		view.Content = string(runes)
		view.Returned = len(runes)
	}
	return view, nil
}

func (f *fakeChannelArtifacts) CanSave(context.Context, string, string, string) error { return nil }

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
	// v2Post carries a newer runbook HTML file the user posted in the channel.
	v2Post *model.Post
	v2File *model.FileInfo
}

func seedArtifactEnv(t *testing.T, suite *TestSuite) *artifactEvalEnv {
	t.Helper()
	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	adminClient := model.NewAPIv4Client(suite.serverURL)
	adminClient.SetToken(suite.adminToken)
	ctx := context.Background()

	upload, _, err := adminClient.UploadFile(ctx, []byte(strings.Replace(runbookHTML, "Priya Raman", "Priya Raman (backup: Omar Haddad)", 1)), data.channel.Id, "migration-cutover-runbook-v2.html")
	require.NoError(t, err)
	require.NotEmpty(t, upload.FileInfos)
	v2Post, _, err := adminClient.CreatePost(ctx, &model.Post{
		ChannelId: data.channel.Id,
		Message:   "Here is the updated cutover runbook, now with a backup rollback owner.",
		FileIds:   []string{upload.FileInfos[0].Id},
	})
	require.NoError(t, err)

	library := newFakeChannelArtifacts()
	return &artifactEvalEnv{
		team:      data.team,
		channel:   data.channel,
		requester: data.alice,
		library:   library,
		runbook:   library.add(data.channel, runbookArtifactName, runbookHTML),
		checklist: library.add(data.channel, checklistName, checklistHTML),
		v2Post:    v2Post,
		v2File:    upload.FileInfos[0],
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

type artifactEvalCase struct {
	name string
	// history is conversation context shown before the user's message.
	history func(env *artifactEvalEnv) string
	message string
	// required lists tools the model must call.
	required []string
	// forbidden lists tools the model must not call.
	forbidden []string
	rubrics   []string
	check     func(e *evals.EvalT, env *artifactEvalEnv, calls []llm.ToolCall, files []createdFile)
}

// runArtifactEval runs one conversation turn through the production system
// prompt and the real MCP tool definitions, with CreateFile beside them.
func runArtifactEval(e *evals.EvalT, suite *TestSuite, env *artifactEvalEnv, c artifactEvalCase) {
	e.Helper()

	setup := setupAgenticEval(e, suite, env.requester, env.team)
	var (
		filesMu sync.Mutex
		files   []createdFile
	)
	setup.llmContext.Tools.AddTools([]llm.Tool{newRecordingCreateFileTool(&filesMu, &files)})
	setup.llmContext.Channel = env.channel
	setup.llmContext.ToolCatalog.ResponseFilesSupported = true

	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(e, err, "Failed to load prompts")
	systemPrompt, err := promptsObj.Format(prompts.PromptDirectMessageQuestionSystem, setup.llmContext)
	require.NoError(e, err, "Failed to format system prompt")

	userMessage := c.message
	if c.history != nil {
		userMessage = c.history(env) + "\n" + c.message
	}
	runner := toolrunner.New(setup.llm)
	runResult, err := runner.Run(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: systemPrompt},
			{Role: llm.PostRoleUser, Message: userMessage},
		},
		Context:   setup.llmContext,
		Operation: llm.OperationConversation,
	}, func(llm.ToolCall) bool { return true }, nil)
	require.NoError(e, err, "ToolRunner should succeed")

	response, err := runResult.Stream.ReadAll()
	require.NoError(e, err, "ReadAll should succeed")
	e.Logf("LLM response:\n%s", response)

	calls := setup.logger.ToolCalls()
	called := setup.logger.CalledTools()
	for _, name := range c.required {
		assert.True(e, slices.Contains(called, name), "%s was not called (called tools: %v)", name, called)
	}
	for _, name := range c.forbidden {
		assert.False(e, slices.Contains(called, name), "%s must not be called (called tools: %v)", name, called)
	}

	filesMu.Lock()
	created := slices.Clone(files)
	filesMu.Unlock()
	if c.check != nil {
		c.check(e, env, calls, created)
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

// threadWithV2File renders the channel's recent posts the way thread context
// reaches the model, including the post and channel IDs.
func threadWithV2File(env *artifactEvalEnv) string {
	var b strings.Builder
	b.WriteString("Conversation so far:\n\n")
	format.WritePost(&b, format.PostEntry{
		HeaderLabel: "Post 1",
		Username:    "bob.eval",
		Post:        env.v2Post,
		ShowChannel: true,
	})
	b.WriteString("Attached files:\n")
	format.WriteFileDescriptor(&b, format.FileDescriptorEntry{Number: 1, FileInfo: env.v2File})
	return b.String()
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
			name:     "builds on a saved page",
			message:  "Take our launch checklist page and turn it into a one-page printable HTML status board for the channel.",
			required: []string{getArtifactTool, createFileToolName},
			check: func(e *evals.EvalT, _ *artifactEvalEnv, _ []llm.ToolCall, files []createdFile) {
				assert.True(e, hasHTMLFile(files), "an HTML file should be created")
				assert.True(e, slices.ContainsFunc(files, func(f createdFile) bool {
					return strings.Contains(f.content, "TLS certificate")
				}), "the new page should carry the saved checklist items")
			},
		},
		{
			name:     "updates a saved page",
			message:  "Add a row for 'Comms lead: Jordan Lee' to the cutover runbook page we have and give me the new version.",
			required: []string{getArtifactTool, createFileToolName},
			check: func(e *evals.EvalT, _ *artifactEvalEnv, _ []llm.ToolCall, files []createdFile) {
				assert.True(e, slices.ContainsFunc(files, func(f createdFile) bool {
					return strings.Contains(f.content, "Jordan Lee") && strings.Contains(f.content, "Priya Raman") && strings.Contains(f.content, "21:15 UTC")
				}), "the new HTML should add Jordan Lee while keeping the saved runbook content")
			},
		},
		{
			name:     "proposes an update from a posted file",
			history:  threadWithV2File,
			message:  "That file is the v2 of our cutover runbook. Can you update the saved runbook page with it?",
			required: []string{proposeUpdateTool},
			check: func(e *evals.EvalT, env *artifactEvalEnv, calls []llm.ToolCall, _ []createdFile) {
				var proposal *llm.ToolCall
				for i := range calls {
					if calls[i].Name == proposeUpdateTool {
						proposal = &calls[i]
					}
				}
				if proposal == nil {
					return
				}
				var args tools.ProposeChannelArtifactUpdateArgs
				require.NoError(e, json.Unmarshal(proposal.Arguments, &args))
				assert.Equal(e, env.runbook.ID, args.ArtifactID, "the runbook, not the checklist, should be targeted")
				assert.Equal(e, env.channel.Id, args.ChannelID)
				assert.Equal(e, env.v2Post.Id, args.PostID)
				assert.Equal(e, env.v2File.Id, args.FileID)
			},
		},
		{
			name:      "does not claim a page was saved to the library",
			message:   "Make a small HTML countdown page for Thursday's 22:00 UTC cutover and save it to the channel's artifact library so the whole team can find it.",
			required:  []string{createFileToolName},
			forbidden: []string{proposeUpdateTool},
			check: func(e *evals.EvalT, _ *artifactEvalEnv, _ []llm.ToolCall, files []createdFile) {
				assert.True(e, hasHTMLFile(files), "an HTML file should be created")
			},
			rubrics: []string{"Does not claim the page is already saved in the channel library. If it mentions the library it says the page still has to be saved from the attached file, for example with a Save action"},
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
			check: func(e *evals.EvalT, _ *artifactEvalEnv, _ []llm.ToolCall, files []createdFile) {
				assert.False(e, hasHTMLFile(files), "a markdown request should not produce an HTML file")
			},
		},
		{
			name:      "negative: searches posts",
			message:   "Find what Charlie said about the data volume.",
			forbidden: artifactToolNames,
			required:  nil,
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
