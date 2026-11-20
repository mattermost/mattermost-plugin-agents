// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// Built-in project tool names.
const (
	ToolUpdateStatusNote  = "update_project_status_note"
	ToolProposeProjectEnd = "propose_project_end"
	ToolStartCursorThread = "start_cursor_thread"
	ToolStartProject      = "start_project"
	ToolLinkCharter       = "link_project_charter"
)

// ProjectEndProposalProp marks the post in which the agent proposes ending
// the project, so the webapp can offer the End project action.
const ProjectEndProposalProp = "project_end_proposal"

const maxStatusNoteLength = 500

type statusNoteArgs struct {
	Note string `json:"note" jsonschema:"One or two sentences on where the project stands."`
}

type linkCharterArgs struct {
	ArtifactID string `json:"artifact_id" jsonschema:"The ID of the charter in the project channel's artifact library."`
}

type proposeEndArgs struct {
	Reason string `json:"reason" jsonschema:"Why the project looks done."`
}

type startCursorThreadArgs struct {
	Title       string `json:"title" jsonschema:"Short title of the coding task."`
	Description string `json:"description" jsonschema:"What the Cursor agent should do, with acceptance criteria."`
	CardID      string `json:"card_id,omitempty" jsonschema:"The board card that tracks this work, if any."`
}

type startProjectArgs struct {
	TeamID      string `json:"team_id" jsonschema:"The team to create the project channel in."`
	DisplayName string `json:"display_name" jsonschema:"The project channel's display name."`
	Purpose     string `json:"purpose,omitempty" jsonschema:"What the project is for."`
	Private     bool   `json:"private,omitempty" jsonschema:"Whether the channel is private."`
}

// ToolsForRequest returns the built-in project tools for one request: the
// project tools for a project agent in its live project channel, and
// start_project for a regular agent in a DM while project channels are on.
// It has the mmtools tool source signature.
func (s *Service) ToolsForRequest(bot *bots.Bot, llmCtx *llm.Context) []llm.Tool {
	if bot == nil || llmCtx == nil || !s.enabled() {
		return nil
	}
	if !bot.IsProjectAgent() {
		if llmCtx.Channel != nil && llmCtx.Channel.Type == model.ChannelTypeDirect &&
			llmCtx.RequestingUser != nil && !llmCtx.RequestingUser.IsGuest() && llmCtx.ToolCatalog.InteractiveUserPresent {
			return []llm.Tool{s.startProjectTool()}
		}
		return nil
	}
	cfg := bot.GetConfig()
	if llmCtx.Channel == nil || llmCtx.Channel.Id != cfg.ProjectChannelID {
		return nil
	}
	project, err := s.deps.Store.GetProject(cfg.ProjectChannelID)
	if err != nil || !project.Live() || project.AgentID != cfg.ID {
		return nil
	}
	tools := []llm.Tool{s.statusNoteTool(project), s.linkCharterTool(project), s.proposeEndTool(project)}
	if llmCtx.ToolCatalog.InteractiveUserPresent && llmCtx.RequestingUser != nil {
		tools = append(tools, s.startCursorThreadTool(bot, project))
	}
	return tools
}

func (s *Service) statusNoteTool(project *store.Project) llm.Tool {
	channelID := project.ChannelID
	return llm.Tool{
		Name:        ToolUpdateStatusNote,
		Description: "Replace the project's status note, shown in the channel header, with one or two sentences on where the project stands.",
		Schema:      llm.NewJSONSchemaFromStruct[statusNoteArgs](),
		AutoExecute: true,
		Resolver: func(ctx context.Context, _ *llm.Context, get llm.ToolArgumentGetter) (string, error) {
			var args statusNoteArgs
			if err := get(&args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			note := strings.TrimSpace(args.Note)
			if note == "" {
				return "", errors.New("the status note can't be empty")
			}
			if len([]rune(note)) > maxStatusNoteLength {
				note = string([]rune(note)[:maxStatusNoteLength])
			}
			if err := s.deps.Store.SetProjectStatusNote(channelID, note); err != nil {
				return "", fmt.Errorf("failed to update the status note: %w", err)
			}
			s.broadcast(channelID)
			return "Status note updated.", nil
		},
	}
}

func (s *Service) linkCharterTool(project *store.Project) llm.Tool {
	channelID := project.ChannelID
	actor := s.ArtifactActor(project)
	return llm.Tool{
		Name:        ToolLinkCharter,
		Description: "Record an artifact saved in the project channel's artifact library as the project charter, so the project panel links to it.",
		Schema:      llm.NewJSONSchemaFromStruct[linkCharterArgs](),
		AutoExecute: true,
		Resolver: func(ctx context.Context, _ *llm.Context, get llm.ToolArgumentGetter) (string, error) {
			var args linkCharterArgs
			if err := get(&args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if s.deps.Artifacts == nil {
				return "", errors.New("the artifact library is not available")
			}
			artifactID := strings.TrimSpace(args.ArtifactID)
			artifactChannelID, err := s.deps.Artifacts.ChannelOf(ctx, actor, artifactID)
			if err != nil {
				return "", fmt.Errorf("failed to find the artifact: %w", err)
			}
			if artifactChannelID != channelID {
				return "", errors.New("the charter must be an artifact in the project channel")
			}
			if err := s.deps.Store.SetProjectArtifact(channelID, false, artifactID); err != nil {
				return "", fmt.Errorf("failed to link the charter: %w", err)
			}
			s.broadcast(channelID)
			return "Charter linked.", nil
		},
	}
}

func (s *Service) proposeEndTool(project *store.Project) llm.Tool {
	channelID := project.ChannelID
	botUserID := project.AgentBotUserID
	return llm.Tool{
		Name:        ToolProposeProjectEnd,
		Description: "Propose ending the project. Posts a proposal the people in the channel can accept with End project; it never ends the project by itself.",
		Schema:      llm.NewJSONSchemaFromStruct[proposeEndArgs](),
		AutoExecute: true,
		Resolver: func(ctx context.Context, llmCtx *llm.Context, get llm.ToolArgumentGetter) (string, error) {
			var args proposeEndArgs
			if err := get(&args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			current, err := s.deps.Store.GetProject(channelID)
			if err != nil {
				return "", fmt.Errorf("failed to check the project: %w", err)
			}
			if current.State != store.ProjectStateActive {
				return "", errors.New("only an active project can be proposed for ending")
			}
			post := &model.Post{
				ChannelId: channelID,
				UserId:    botUserID,
				Message:   "I think this project is ready to wrap up: " + strings.TrimSpace(args.Reason) + "\n\nThe project lead can end it from the channel header.",
			}
			post.AddProp(ProjectEndProposalProp, true)
			if err := s.deps.Platform.CreatePost(post); err != nil {
				return "", fmt.Errorf("failed to post the proposal: %w", err)
			}
			return "Proposal posted. The project ends only when someone in the channel ends it.", nil
		},
	}
}

func (s *Service) startCursorThreadTool(bot *bots.Bot, project *store.Project) llm.Tool {
	channelID := project.ChannelID
	return llm.Tool{
		Name:        ToolStartCursorThread,
		Description: "Open a run thread in the project channel for a Cursor cloud agent working on one coding task. The Cursor run itself is started from that thread with the requester's approval.",
		Schema:      llm.NewJSONSchemaFromStruct[startCursorThreadArgs](),
		// The requester approves the title and description that seed the run.
		AlwaysAsk: true,
		Resolver: func(ctx context.Context, llmCtx *llm.Context, get llm.ToolArgumentGetter) (string, error) {
			var args startCursorThreadArgs
			if err := get(&args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			title := strings.TrimSpace(args.Title)
			if title == "" {
				return "", errors.New("a run thread needs a title")
			}
			if llmCtx == nil || llmCtx.RequestingUser == nil {
				return "", errors.New("a run thread needs a requester")
			}
			if llmCtx.Channel == nil || llmCtx.Channel.Id != channelID {
				return "", ErrToolOutsideProject
			}
			current, err := s.deps.Store.GetProject(channelID)
			if err != nil {
				return "", fmt.Errorf("failed to check the project: %w", err)
			}
			if current.State != store.ProjectStateActive && current.State != store.ProjectStateSetup {
				return "", errors.New("the project is not running")
			}
			thread, err := s.deps.Threads.StartAgentThread(context.WithoutCancel(ctx), conversations.AgentThreadRequest{
				Bot:         bot,
				User:        llmCtx.RequestingUser,
				Channel:     llmCtx.Channel,
				Operation:   llm.OperationProjectRun,
				Message:     "Cursor run: " + title,
				Instruction: runKickoffInstruction(args.Description),
				RootProps:   map[string]any{conversations.ProjectThreadOperationProp: llm.OperationProjectRun},
			})
			if err != nil {
				return "", fmt.Errorf("failed to start the run thread: %w", err)
			}
			if err := s.deps.Store.AddProjectThread(&store.ProjectThread{
				RootPostID: thread.RootPost.Id,
				ChannelID:  channelID,
				Kind:       store.ProjectThreadKindCursor,
				CardID:     strings.TrimSpace(args.CardID),
				StartedBy:  llmCtx.RequestingUser.Id,
				CreateAt:   model.GetMillis(),
			}); err != nil {
				return "", fmt.Errorf("failed to record the run thread: %w", err)
			}
			return fmt.Sprintf("Run thread started (root post %s).", thread.RootPost.Id), nil
		},
	}
}

func (s *Service) startProjectTool() llm.Tool {
	return llm.Tool{
		Name:        ToolStartProject,
		Description: "Create a project channel with its own project agent for the user, from this conversation. Ask for the team and a name first.",
		Schema:      llm.NewJSONSchemaFromStruct[startProjectArgs](),
		Resolver: func(ctx context.Context, llmCtx *llm.Context, get llm.ToolArgumentGetter) (string, error) {
			var args startProjectArgs
			if err := get(&args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if llmCtx == nil || llmCtx.RequestingUser == nil {
				return "", errors.New("starting a project needs a requester")
			}
			project, err := s.CreateProject(context.WithoutCancel(ctx), llmCtx.RequestingUser.Id, CreateRequest{
				TeamID:      args.TeamID,
				DisplayName: args.DisplayName,
				Purpose:     args.Purpose,
				Private:     args.Private,
				CreatedFrom: store.ProjectCreatedFromDM,
			})
			if err != nil {
				return "", fmt.Errorf("failed to start the project: %w", err)
			}
			return fmt.Sprintf("Project channel created (channel %s). Setup continues in the channel.", project.ChannelID), nil
		},
	}
}
