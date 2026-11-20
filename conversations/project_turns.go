// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrProjectTurnRefused is returned when a project agent's turn may not run:
// outside its project channel, in a DM, while its project is not live or
// cannot run scoped, or for a requester the project cannot act for.
var ErrProjectTurnRefused = errors.New("this project agent can't reply here")

// ProjectGate decides whether a project agent's turn may run. It fails closed.
type ProjectGate interface {
	// CheckTurn returns nil when bot may run a turn in channel for user.
	CheckTurn(ctx context.Context, bot *bots.Bot, channel *model.Channel, user *model.User) error
	// SystemPromptSuffix is the project block every turn of bot sees, built
	// from the project's current state, for a conversation of operation.
	SystemPromptSuffix(ctx context.Context, bot *bots.Bot, operation string) string
}

// SetProjectGate installs the projects service.
func (c *Conversations) SetProjectGate(gate ProjectGate) {
	c.operationsMu.Lock()
	defer c.operationsMu.Unlock()
	c.projectGate = gate
}

func (c *Conversations) projects() ProjectGate {
	c.operationsMu.RLock()
	defer c.operationsMu.RUnlock()
	return c.projectGate
}

// checkProjectTurn refuses a project agent's turn anywhere but its project
// channel, and wherever the projects service refuses it.
func (c *Conversations) checkProjectTurn(ctx context.Context, req turnRequest, isDM bool) error {
	if !req.bot.IsProjectAgent() {
		return nil
	}
	gate := c.projects()
	if gate == nil || isDM || req.channel.Id != req.bot.GetConfig().ProjectChannelID {
		return ErrProjectTurnRefused
	}
	if err := gate.CheckTurn(ctx, req.bot, req.channel, req.user); err != nil {
		return fmt.Errorf("%w: %w", ErrProjectTurnRefused, err)
	}
	return nil
}

// ProjectSystemPromptSuffix returns the project block for bot's turns, or ""
// for agents that do not lead a project.
func (c *Conversations) ProjectSystemPromptSuffix(ctx context.Context, botID, operation string) string {
	gate := c.projects()
	if gate == nil || c.bots == nil || botID == "" {
		return ""
	}
	bot := c.bots.GetBotByID(botID)
	if bot == nil || !bot.IsProjectAgent() {
		return ""
	}
	return gate.SystemPromptSuffix(ctx, bot, operation)
}

// projectRefusalNotice tells the person whose post was refused why the
// project agent did not answer, without revealing anything about the project.
func (c *Conversations) projectRefusalNotice(bot *bots.Bot, post *model.Post) {
	if post == nil || post.UserId == "" {
		return
	}
	c.mmClient.SendEphemeralPost(post.UserId, &model.Post{
		ChannelId: post.ChannelId,
		RootId:    post.RootId,
		UserId:    bot.BotUserID(),
		Message:   "I can't reply to this right now. A project agent only works in its project channel while the project is set up for it; ask a system admin to check the project's prerequisites.",
	})
}

// dropAlwaysAskTools removes tools that need a person's approval from a turn
// nobody is present to approve, such as starting a Cursor run.
func dropAlwaysAskTools(llmContext *llm.Context) {
	if llmContext == nil || llmContext.Tools == nil {
		return
	}
	llmContext.Tools.KeepToolsIf(func(tool llm.Tool) bool { return !tool.AlwaysAsk })
}

// keepReadOnlyTools leaves an unattended run-thread turn, whose task came
// from the model, only tools that read until a person replies in the thread.
func keepReadOnlyTools(llmContext *llm.Context) {
	if llmContext == nil || llmContext.Tools == nil {
		return
	}
	llmContext.Tools.KeepToolsIf(func(tool llm.Tool) bool { return tool.ReadOnly })
}

// ProjectThreadOperationProp records, on the root post of a thread a project
// agent started, the conversation operation replies in the thread continue.
// Only roots the agent itself posted are trusted.
const ProjectThreadOperationProp = "project_thread_operation"

// projectThreadOperation returns the operation of the project thread req
// replies in, or "" when it is not in a thread the agent started with a
// registered operation.
func (c *Conversations) projectThreadOperation(req turnRequest) (string, error) {
	rootID := turnThreadRootID(req)
	if rootID == "" {
		return "", nil
	}
	root, err := c.mmClient.GetPost(rootID)
	if err != nil {
		return "", fmt.Errorf("failed to get thread root: %w", err)
	}
	if root.UserId != req.bot.BotUserID() || root.ChannelId != req.channel.Id {
		return "", nil
	}
	op, _ := root.GetProp(ProjectThreadOperationProp).(string)
	if _, registered := c.registeredOperation(op); !registered {
		return "", nil
	}
	return op, nil
}
