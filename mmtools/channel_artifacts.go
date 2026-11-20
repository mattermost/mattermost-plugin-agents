// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// SaveChannelArtifactToolName is the runtime name of the library save tool.
	SaveChannelArtifactToolName = "SaveChannelArtifact"
	// RemoveChannelArtifactToolName is the runtime name of the library removal tool.
	RemoveChannelArtifactToolName = "RemoveChannelArtifact"

	saveChannelArtifactDescription = "Save a document to this channel's Channel artifacts library, or add a new version to a saved artifact. " +
		"Use when the user asks you to save, pin or keep a dashboard, calculator, report, charter, summary or other document for the channel, or to update one that is already saved. " +
		"Set format to html for interactive, self-contained HTML (the default) or markdown for a written document. " +
		"Provide either content (the complete document; it is posted to the channel as a message from you with the file attached, and that message is what the library keeps) " +
		"or file_id (an HTML or Markdown file already attached to one of your posts in this channel). " +
		"Pass artifact_id to add a version to an existing artifact (find ids with list_channel_artifacts); omit it to create a new one. An artifact keeps its format. " +
		"Only works in the current channel and only when the user may manage this channel's bookmarks. " +
		"The library stores a reference to the message, not a copy: the artifact stays available only while that message and its file exist."
	removeChannelArtifactDescription = "Remove a saved artifact from this channel's Channel artifacts library. " +
		"Only the library entry is removed; the original message and file are not deleted. " +
		"Only works in the current channel and only when the user may delete this channel's bookmarks."
)

// SaveChannelArtifactArgs is the LLM-visible input schema for SaveChannelArtifact.
type SaveChannelArtifactArgs struct {
	Title      string `json:"title,omitempty" jsonschema_description:"Display name, at most 64 characters. Required for a new artifact; optional when adding a version"`
	Format     string `json:"format,omitempty" jsonschema_description:"html (the default) or markdown. Omit when adding a version or registering a file; both keep the format they already have"`
	Content    string `json:"content,omitempty" jsonschema_description:"Complete HTML or Markdown document to post and save. Provide this or file_id"`
	FileID     string `json:"file_id,omitempty" jsonschema_description:"ID of an HTML or Markdown file already attached to one of your posts in this channel. Provide this or content"`
	ArtifactID string `json:"artifact_id,omitempty" jsonschema_description:"Existing artifact to add a version to; omit to create a new artifact"`
}

// RemoveChannelArtifactArgs is the LLM-visible input schema for RemoveChannelArtifact.
type RemoveChannelArtifactArgs struct {
	ArtifactID string `json:"artifact_id" jsonschema_description:"The artifact to remove from the library"`
}

// ChannelArtifactResult is the tool result content.
type ChannelArtifactResult struct {
	ArtifactID string `json:"artifact_id"`
	Title      string `json:"title,omitempty"`
	Format     string `json:"format,omitempty"`
	Version    int    `json:"version,omitempty"`
	PostID     string `json:"post_id,omitempty"`
	Note       string `json:"note,omitempty"`
}

// ChannelArtifactTools gives the agent write access to the channel library.
// Every write is bound to the request's channel and checked against the
// writing user's channel bookmark permissions.
type ChannelArtifactTools struct {
	Library channelartifacts.Library
	// Enabled reports whether the library is on; read on every GetTools call.
	Enabled func() bool
	// ActorFor overrides who the library write is made as. By default it is
	// the requesting user, with the agent as the author and the request's
	// session when it has one. Return false to refuse the call. When it is
	// set, a request needs no requesting user.
	ActorFor func(ctx context.Context, llmCtx *llm.Context) (channelartifacts.Actor, bool)
	// AutoExecuteFor reports whether the named tool runs without approval for
	// this request. Nil, the default, means every call asks. It is read when
	// the request's tools are built, so it applies wherever a turn starts or
	// resumes.
	AutoExecuteFor func(llmCtx *llm.Context, toolName string) bool
	// Audit records a library change made by the agent on behalf of userID.
	// status is a model.AuditStatus* value; only object IDs are passed.
	Audit func(event, status, userID, channelID, artifactID string)
}

func (t *ChannelArtifactTools) available(llmCtx *llm.Context) bool {
	if t == nil || t.Library == nil || t.Enabled == nil || !t.Enabled() ||
		llmCtx == nil || llmCtx.Channel == nil || !channelartifacts.SupportsChannelType(llmCtx.Channel.Type) ||
		!model.IsValidId(llmCtx.BotUserID) {
		return false
	}
	return t.ActorFor != nil || llmCtx.RequestingUser != nil
}

func (t *ChannelArtifactTools) actor(ctx context.Context, llmCtx *llm.Context) (channelartifacts.Actor, bool) {
	if t.ActorFor != nil {
		actor, ok := t.ActorFor(ctx, llmCtx)
		return actor, ok && actor.UserID != ""
	}
	return channelartifacts.Actor{
		UserID:    llmCtx.RequestingUser.Id,
		BotUserID: llmCtx.BotUserID,
		SessionID: auth.SessionIDFromContext(ctx),
	}, true
}

func (t *ChannelArtifactTools) autoExecute(llmCtx *llm.Context, toolName string) bool {
	return t.AutoExecuteFor != nil && t.AutoExecuteFor(llmCtx, toolName)
}

// Tools returns the library tools for the request, or none while the library is
// off, the request is not in a public or private channel, or it has no writer
// or agent.
func (t *ChannelArtifactTools) Tools(llmCtx *llm.Context) []llm.Tool {
	if !t.available(llmCtx) {
		return nil
	}
	return []llm.Tool{
		{
			Name:        SaveChannelArtifactToolName,
			Description: saveChannelArtifactDescription,
			Schema:      llm.NewJSONSchemaFromStruct[SaveChannelArtifactArgs](),
			AutoExecute: t.autoExecute(llmCtx, SaveChannelArtifactToolName),
			Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				return t.resolveSave(ctx, llmCtx, argsGetter)
			},
		},
		{
			Name:        RemoveChannelArtifactToolName,
			Description: removeChannelArtifactDescription,
			Schema:      llm.NewJSONSchemaFromStruct[RemoveChannelArtifactArgs](),
			AutoExecute: t.autoExecute(llmCtx, RemoveChannelArtifactToolName),
			Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				return t.resolveRemove(ctx, llmCtx, argsGetter)
			},
		},
	}
}

func (t *ChannelArtifactTools) audit(event, status, userID string, llmCtx *llm.Context, artifactID string) {
	if t.Audit != nil {
		t.Audit(event, status, userID, llmCtx.Channel.Id, artifactID)
	}
}

func artifactToolUserMessage(err error) string {
	switch {
	case errors.Is(err, channelartifacts.ErrForbidden), errors.Is(err, channelartifacts.ErrNotFound):
		return "the writing user may not manage channel artifacts here or download that file, or the artifact was not found in this channel"
	case errors.Is(err, channelartifacts.ErrSessionRequired):
		return "the requesting user's session is not available, so their file permissions can't be checked; save content instead of a file_id"
	case errors.Is(err, channelartifacts.ErrLimitReached):
		return "this channel's artifact library is full; remove one first"
	case errors.Is(err, channelartifacts.ErrDuplicate):
		return "an artifact with identical content is already saved in this channel"
	case errors.Is(err, channelartifacts.ErrTooLarge):
		return "the document is too large to save"
	case errors.Is(err, channelartifacts.ErrIneligible):
		return "the file must be an HTML or Markdown file attached to one of your posts in this channel, in the same format as the artifact it updates"
	case errors.Is(err, channelartifacts.ErrInvalidInput):
		return "invalid parameters: check title (1-64 characters), format, ids and that the artifact's format matches"
	default:
		return "saving the artifact failed"
	}
}

func (t *ChannelArtifactTools) resolveSave(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args SaveChannelArtifactArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for %s: %w", SaveChannelArtifactToolName, err)
	}
	if !t.available(llmCtx) {
		return "channel artifacts are not available here", errors.New("channel artifacts unavailable")
	}
	actor, ok := t.actor(ctx, llmCtx)
	if !ok {
		return artifactToolUserMessage(channelartifacts.ErrForbidden), channelartifacts.ErrForbidden
	}
	if (args.Content == "") == (args.FileID == "") {
		return "provide exactly one of content or file_id", errors.New("SaveChannelArtifact needs content or file_id")
	}
	if args.ArtifactID != "" && !model.IsValidId(args.ArtifactID) {
		return "artifact_id is not a valid id", errors.New("invalid artifact_id")
	}
	if args.ArtifactID == "" && strings.TrimSpace(args.Title) == "" && args.Content != "" {
		return "title is required for a new artifact", errors.New("SaveChannelArtifact missing title")
	}
	format := channelartifacts.Format(strings.ToLower(strings.TrimSpace(args.Format)))
	if format != "" && !format.Valid() {
		return "format must be html or markdown", errors.New("invalid format")
	}

	req := channelartifacts.WriteRequest{
		ChannelID: llmCtx.Channel.Id, FileID: args.FileID, Title: args.Title, ArtifactID: args.ArtifactID, Format: format,
		Body: []byte(args.Content),
	}
	req.ThreadOfPostID = llmCtx.ResponsePostID
	entry, err := t.Library.Write(ctx, actor, req)
	if err != nil {
		t.audit("saveChannelArtifactByAgent", model.AuditStatusFail, actor.UserID, llmCtx, args.ArtifactID)
		return artifactToolUserMessage(err), fmt.Errorf("SaveChannelArtifact failed: %w", err)
	}
	t.audit("saveChannelArtifactByAgent", model.AuditStatusSuccess, actor.UserID, llmCtx, entry.ID)

	out, err := json.Marshal(ChannelArtifactResult{
		ArtifactID: entry.ID, Title: entry.Title, Format: string(entry.Format), Version: entry.Version, PostID: entry.SourcePostID,
		Note: "Saved. The library keeps a reference to the message with the file, so the artifact lasts only as long as that message does.",
	})
	if err != nil {
		return "saved", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(out), nil
}

func (t *ChannelArtifactTools) resolveRemove(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args RemoveChannelArtifactArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for %s: %w", RemoveChannelArtifactToolName, err)
	}
	if !t.available(llmCtx) {
		return "channel artifacts are not available here", errors.New("channel artifacts unavailable")
	}
	if !model.IsValidId(args.ArtifactID) {
		return "artifact_id is not a valid id", errors.New("invalid artifact_id")
	}
	actor, ok := t.actor(ctx, llmCtx)
	if !ok {
		return artifactToolUserMessage(channelartifacts.ErrForbidden), channelartifacts.ErrForbidden
	}
	entry, err := t.Library.Get(ctx, actor, args.ArtifactID)
	if err != nil || entry.ChannelID != llmCtx.Channel.Id {
		return artifactToolUserMessage(channelartifacts.ErrNotFound), errors.New("artifact not in this channel")
	}
	if _, err = t.Library.Remove(ctx, actor, args.ArtifactID); err != nil {
		t.audit("removeChannelArtifactByAgent", model.AuditStatusFail, actor.UserID, llmCtx, args.ArtifactID)
		return artifactToolUserMessage(err), fmt.Errorf("RemoveChannelArtifact failed: %w", err)
	}
	t.audit("removeChannelArtifactByAgent", model.AuditStatusSuccess, actor.UserID, llmCtx, args.ArtifactID)
	out, err := json.Marshal(ChannelArtifactResult{ArtifactID: args.ArtifactID, Note: "Removed from the library. The original message and file were not deleted."})
	if err != nil {
		return "removed", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(out), nil
}
