// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/evals"
	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// runAgenticFlowEval is a helper that runs an agentic flow eval with the given
// prompt and rubrics. Each entry of requiredTools lists alternative tools, at
// least one of which the LLM must have called.
func runAgenticFlowEval(e *evals.EvalT, suite *TestSuite, requestingUser *model.User, team *model.Team, userMessage string, rubrics []string, requiredTools [][]string) string {
	e.Helper()

	setup := setupAgenticEval(e, suite, requestingUser, team)

	promptsObj, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(e, err, "Failed to load prompts")

	systemPrompt, err := promptsObj.Format(prompts.PromptDirectMessageQuestionSystem, setup.llmContext)
	require.NoError(e, err, "Failed to format system prompt")

	posts := []llm.Post{
		{Role: llm.PostRoleSystem, Message: systemPrompt},
		{Role: llm.PostRoleUser, Message: userMessage},
	}

	runner := toolrunner.New(setup.llm)
	runResult, err := runner.Run(context.Background(), llm.CompletionRequest{
		Posts:     posts,
		Context:   setup.llmContext,
		Operation: llm.OperationConversation,
	}, func(tc llm.ToolCall) bool { return true }, nil)
	require.NoError(e, err, "ToolRunner should succeed")

	response, err := runResult.Stream.ReadAll()
	require.NoError(e, err, "ReadAll should succeed")
	require.NotEmpty(e, response, "Response should not be empty")

	e.Logf("LLM response:\n%s", response)

	// Assert that the required tools were actually called (not just that the LLM claims it was)
	calledTools := setup.logger.CalledTools()
	for _, alternatives := range requiredTools {
		found := slices.ContainsFunc(alternatives, func(tool string) bool { return slices.Contains(calledTools, tool) })
		assert.True(e, found, "None of the tools %v was called by the LLM (called tools: %v)", alternatives, calledTools)
	}

	for _, rubric := range rubrics {
		evals.LLMRubricT(e, rubric, response)
	}

	return response
}

// dmsSince returns the messages sender posted in its DM channel with recipient
// at or after since. Every trial shares one server, so earlier trials' DMs must
// not count for the current one.
func dmsSince(e *evals.EvalT, client *model.Client4, sender, recipient *model.User, since int64) []string {
	e.Helper()
	ctx := context.Background()

	channel, _, err := client.CreateDirectChannel(ctx, sender.Id, recipient.Id)
	require.NoError(e, err, "Should get DM channel with %s", recipient.Username)

	posts, _, err := client.GetPostsForChannel(ctx, channel.Id, 0, 100, "", false, false)
	require.NoError(e, err, "Should get DM posts for %s", recipient.Username)

	var messages []string
	for _, id := range posts.Order {
		post := posts.Posts[id]
		if post.UserId == sender.Id && post.CreateAt >= since {
			messages = append(messages, post.Message)
		}
	}
	return messages
}

func mentionsAllHands(messages []string) bool {
	return slices.ContainsFunc(messages, func(m string) bool { return strings.Contains(strings.ToLower(m), "all-hands") })
}

// ---------------------------------------------------------------------------
// Agentic Flow Evals
// ---------------------------------------------------------------------------

// TestChannelSummarizationFlowEval tests the full agentic loop:
// LLM discovers channel by display name → calls read_channel → produces summary.
func TestChannelSummarizationFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	evals.Run(t, "channel summarization flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, data.alice, data.team,
			// Use all-lowercase to test case-insensitive lookup and substring matching ("eval" → "Eval Team")
			"Summarize what's been discussed in the migration planning channel on the eval team.",
			[]string{
				"Mentions a database migration (e.g., MySQL to PostgreSQL)",
				"Mentions at least two of: timeline/sprint, data volume/downtime, rollback plan",
				"Is a coherent summary, not a raw dump of messages or tool output",
				// Rubric referencing a user that only exists in the seeded data (anti-hallucination)
				"Attributes at least one point to a participant (alice.eval, bob.eval, or charlie.eval), by username or by name",
			},
			// read_channel needs the channel ID, so calling it shows the channel was found;
			// any of the channel lookup tools may have found it.
			[][]string{{"read_channel"}},
		)
	})
}

// TestFindSpecificInfoFlowEval tests search → synthesize:
// LLM must discover the right channel or post on its own and find what a specific user said.
func TestFindSpecificInfoFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)

	evals.Run(t, "find specific info flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, data.alice, data.team,
			"What did alice.eval say about the rollback plan for the database migration?",
			[]string{
				"Mentions keeping MySQL in read-only mode during cutover",
				"Mentions the ability to switch back within minutes",
				"Mentions continuous replication as a safety net",
				"Does not attribute the rollback plan to bob.eval or charlie.eval",
				"Does not present the migration schedule (next sprint, two weeks of testing), the data volume, or the monitoring dashboards as part of the rollback plan",
			},
			[][]string{{"read_channel", "search_posts", "read_post"}},
		)
	})
}

// TestDMSummaryFlowEval tests the read → write loop:
// LLM discovers channel by display name → reads it → composes a summary → sends it as a DM.
func TestDMSummaryFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedChannelConversation(t, suite.serverURL, suite.adminToken)
	adminClient := model.NewAPIv4Client(suite.serverURL)
	adminClient.SetToken(suite.adminToken)
	// The MCP server acts as the admin, so the DM comes from the admin.
	adminUser := getAdminUser(t, suite)

	evals.Run(t, "dm summary flow", func(e *evals.EvalT) {
		start := model.GetMillis()
		runAgenticFlowEval(e, suite, data.alice, data.team,
			// Use wrong capitalization on "Planning" to test case-insensitive lookup
			"Send bob.eval a DM summarizing the key decisions made in the migration Planning channel on the Eval team.",
			[]string{
				"Confirms that a DM was sent to bob.eval, referring to him by username or by name",
			},
			[][]string{{"read_channel"}, {"dm"}},
		)

		dms := dmsSince(e, adminClient, adminUser, data.bob, start)
		require.NotEmpty(e, dms, "A DM should have been sent to bob")
		dm := strings.Join(dms, "\n\n")
		e.Logf("DM to bob:\n%s", dm)
		evals.LLMRubricT(e, "Summarizes a planned migration from MySQL to PostgreSQL and at least one of: the timeline, the data volume or downtime concern, or the rollback plan", dm)
	})
}

// TestBroadcastDMToTeamFlowEval tests the team lookup → list members → filter bots → send DMs flow:
// LLM looks up a team by display name, gets members, excludes bot accounts, and DMs each real user.
func TestBroadcastDMToTeamFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()
	suite.CreateMCPServer(false)

	data := seedTeamBroadcastScenario(t, suite.serverURL, suite.adminToken)
	adminClient := model.NewAPIv4Client(suite.serverURL)
	adminClient.SetToken(suite.adminToken)
	// The MCP server acts as the admin, who is also a team member.
	adminUser := getAdminUser(t, suite)

	evals.Run(t, "broadcast DM to team flow", func(e *evals.EvalT) {
		start := model.GetMillis()
		runAgenticFlowEval(e, suite, data.dana, data.team,
			// Use lowercase "staff" to test case-insensitive team lookup
			"Send everyone on the staff team a direct message saying: Hey! Just a reminder that we have a company all-hands meeting tomorrow at 3pm in the main conference room. Please make sure to attend.",
			[]string{
				"Confirms that DMs were sent and identifies the recipients",
				"Does not claim to have sent a message to a bot account (autobot.eval or Automation Bot)",
			},
			[][]string{{"dm"}},
		)

		// The request comes from dana while the tools act as the admin, so the
		// model may reasonably skip either as "yourself".
		for _, member := range []*model.User{data.emma, data.frank} {
			assert.True(e, mentionsAllHands(dmsSince(e, adminClient, adminUser, member, start)),
				"A DM about the all-hands meeting should have been sent to %s", member.Username)
		}
		assert.False(e, mentionsAllHands(dmsSince(e, adminClient, adminUser, data.botUser, start)),
			"A DM about the all-hands meeting should NOT have been sent to bot %s", data.botUser.Username)
	})
}

// ---------------------------------------------------------------------------
// read_file Evals
//
// These exercise the lazy file-loading flow: an attachment is shown to the LLM
// as a metadata descriptor (what conversation assembly emits for a large file),
// and the LLM must call read_file to fetch the contents on demand. The user
// asks are intentionally vague — no file names, IDs, or tool hints — so the
// model has to connect intent to the attached file itself.
// ---------------------------------------------------------------------------

// getAdminUser returns the user the MCP session authenticates as (the file
// content service trusts this identity for reads).
func getAdminUser(t *testing.T, suite *TestSuite) *model.User {
	t.Helper()
	client := model.NewAPIv4Client(suite.serverURL)
	client.SetToken(suite.adminToken)
	user, _, err := client.GetMe(context.Background(), "")
	require.NoError(t, err, "Failed to get admin user")
	return user
}

// withFileDescriptor appends the production-style attached-file metadata block
// (what conversation assembly shows the LLM for a non-inlined file) to a user
// message, so the eval prompt matches what the model sees in production.
func withFileDescriptor(userMessage string, fileInfo *model.FileInfo) string {
	var b strings.Builder
	b.WriteString(userMessage)
	b.WriteString("\n\nAttached files (call the read_file tool with the File ID to read their contents):\n")
	format.WriteFileDescriptor(&b, format.FileDescriptorEntry{Number: 1, FileInfo: fileInfo})
	return b.String()
}

func fileEvalService(suite *TestSuite) *client4FileContentService {
	return &client4FileContentService{serverURL: suite.serverURL, token: suite.adminToken}
}

// TestReadFileOnDemandFlowEval: given only metadata for an attached checklist,
// the LLM must read it and surface a fact that exists only inside the file.
func TestReadFileOnDemandFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()

	var checklist strings.Builder
	checklist.WriteString(strings.Join([]string{
		"Pre-launch checklist:",
		"1. Freeze the deploy pipeline at 5pm Friday.",
		"2. Notify the support team about the maintenance window.",
		"3. Rotate the staging TLS certificate before the cutover.",
		"4. Snapshot the primary database.",
		"5. Update the public status page once traffic is migrated.",
		"",
		"Launch-day notes:",
	}, "\n"))
	// Production inlines small text files, so the checklist is padded past the
	// inline limit to be shown as a descriptor, as a real large file would be.
	for i := 1; checklist.Len() <= int(conversation.InlineFileMaxBytes); i++ {
		fmt.Fprintf(&checklist, "\n- Note %d: confirm the on-call rotation and dashboard links for shard %d are current.", i, i)
	}

	team, _, fileInfo := seedFileScenario(t, suite.serverURL, suite.adminToken, "launch-checklist.txt", []byte(checklist.String()))
	suite.CreateMCPServerWithFileService(false, fileEvalService(suite))
	adminUser := getAdminUser(t, suite)

	evals.Run(t, "read_file on-demand flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, adminUser, team,
			withFileDescriptor(
				// Vague, real-user phrasing: no file name, no ID, no mention of tools.
				"I'm prepping for the Friday launch and I have a nagging feeling there's something I'm supposed to do with our certs beforehand. Can you check and remind me?",
				fileInfo,
			),
			[]string{
				"Mentions rotating (or renewing) the staging TLS certificate before the cutover",
				"The answer is grounded in the attached checklist rather than generic launch advice",
				"Does not claim it is unable to see, open, or read the attached file",
			},
			[][]string{{"read_file"}},
		)
	})
}

// TestReadFilePagingFlowEval: the answer lives past a single read window, so the
// LLM must page with offset to find it.
func TestReadFilePagingFlowEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()

	// Build a log well past the max single-read window so the model cannot get
	// the planted crash in one call and must page with offset to reach the end.
	var log strings.Builder
	for i := range 1500 {
		fmt.Fprintf(&log, "2026-05-01T10:%02d:%02dZ [info] request %d handled in %dms\n", i%60, i%60, i, i%200)
	}
	log.WriteString("2026-05-01T11:00:00Z [error] worker crashed: correlation_id=zephyr-9931-omega\n")
	for range 50 {
		log.WriteString("2026-05-01T11:00:01Z [info] worker shutting down\n")
	}
	require.Greater(t, len([]rune(log.String())), files.MaxReadRunes, "log must exceed one read window to force paging")

	team, _, fileInfo := seedFileScenario(t, suite.serverURL, suite.adminToken, "server-debug.txt", []byte(log.String()))
	suite.CreateMCPServerWithFileService(false, fileEvalService(suite))
	adminUser := getAdminUser(t, suite)

	evals.Run(t, "read_file paging flow", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, adminUser, team,
			withFileDescriptor(
				"A worker crashed somewhere in that debug log I shared. Dig through it and tell me the correlation ID of the crash so I can find it in our tracing.",
				fileInfo,
			),
			[]string{
				"States the correlation ID zephyr-9931-omega",
				"Does not give a different correlation ID or claim that no crash could be found",
			},
			[][]string{{"read_file"}},
		)
	})
}

// TestReadFileUnreadableBinaryEval: a binary attachment has no extractable text,
// so the LLM must say it cannot read it instead of inventing contents.
func TestReadFileUnreadableBinaryEval(t *testing.T) {
	evals.NumEvalsOrSkip(t)

	suite := SetupTestSuite(t)
	defer suite.TearDown()

	// A .bin file is stored as application/octet-stream, which has no
	// server-extractable text, so production shows it as a descriptor and
	// read_file reports no readable content. Images are never descriptors.
	binBytes := []byte{0x7f, 0x45, 0x4c, 0x46, 0x02, 0x01, 0x01, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	team, _, fileInfo := seedFileScenario(t, suite.serverURL, suite.adminToken, "firmware.bin", binBytes)
	suite.CreateMCPServerWithFileService(false, fileEvalService(suite))
	adminUser := getAdminUser(t, suite)

	evals.Run(t, "read_file unreadable binary", func(e *evals.EvalT) {
		runAgenticFlowEval(e, suite, adminUser, team,
			withFileDescriptor(
				"Can you take a look at the file I just dropped in and give me a quick rundown of what's in it?",
				fileInfo,
			),
			[]string{
				"Indicates it cannot read or extract the text contents of the file",
				"Does not invent or describe specific contents of the file",
			},
			// The descriptor already says the file is binary, so the model may
			// answer without calling read_file.
			nil,
		)
	})
}
