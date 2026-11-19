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
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// SaveChannelArtifactToolName is the runtime name of the library save tool.
	SaveChannelArtifactToolName = "SaveChannelArtifact"
	// RemoveChannelArtifactToolName is the runtime name of the library removal tool.
	RemoveChannelArtifactToolName = "RemoveChannelArtifact"

	saveChannelArtifactDescription = "Save interactive HTML to this channel's Channel artifacts library, or add a new version to a saved artifact. " +
		"Use when the user asks you to save, pin or keep a dashboard, calculator, report or other HTML app for the channel, or to update one that is already saved. " +
		"Provide either content (the complete self-contained HTML; it is posted to the channel as a message from you with the file attached, and that message is what the library keeps) " +
		"or file_id (an HTML file already attached to one of your posts in this channel). " +
		"Pass artifact_id to add a version to an existing artifact (find ids with list_channel_artifacts); omit it to create a new one. " +
		"Only works in the current channel and only when the user may manage this channel's bookmarks. " +
		"The library stores a reference to the message, not a copy: the artifact stays available only while that message and its file exist."
	removeChannelArtifactDescription = "Remove a saved artifact from this channel's Channel artifacts library. " +
		"Only the library entry is removed; the original message and file are not deleted. " +
		"Only works in the current channel and only when the user may delete this channel's bookmarks."
)

// SaveChannelArtifactArgs is the LLM-visible input schema for SaveChannelArtifact.
type SaveChannelArtifactArgs struct {
	Title      string `json:"title,omitempty" jsonschema_description:"Display name, at most 64 characters. Required for a new artifact; optional when adding a version"`
	Content    string `json:"content,omitempty" jsonschema_description:"Complete self-contained HTML to post and save. Provide this or file_id"`
	FileID     string `json:"file_id,omitempty" jsonschema_description:"ID of an HTML file already attached to one of your posts in this channel. Provide this or content"`
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
	Version    int    `json:"version,omitempty"`
	PostID     string `json:"post_id,omitempty"`
	Note       string `json:"note,omitempty"`
}

// ChannelArtifactTools gives the agent write access to the channel library on
// behalf of the requesting user. Every write is bound to the request's
// channel and checked against that user's channel bookmark permissions.
type ChannelArtifactTools struct {
	Service *channelartifacts.Service
	Client  mmapi.Client
	// Enabled reports whether the library is on; read on every GetTools call.
	Enabled func() bool
	// Limits returns the current caps for writes.
	Limits func() channelartifacts.Limits
	// Audit records a library change made by the agent on behalf of userID.
	// status is a model.AuditStatus* value; only object IDs are passed.
	Audit func(event, status, userID, channelID, artifactID string)
}

func (t *ChannelArtifactTools) available(llmCtx *llm.Context) bool {
	return t != nil && t.Service != nil && t.Enabled != nil && t.Enabled() &&
		llmCtx != nil && llmCtx.Channel != nil && channelartifacts.SupportsChannelType(llmCtx.Channel.Type) &&
		llmCtx.RequestingUser != nil && model.IsValidId(llmCtx.BotUserID)
}

// Tools returns the library tools for the request, or none while the library is
// off, the request is not in a public or private channel, or it has no
// requesting user or agent.
func (t *ChannelArtifactTools) Tools(llmCtx *llm.Context) []llm.Tool {
	if !t.available(llmCtx) {
		return nil
	}
	return []llm.Tool{
		{
			Name:        SaveChannelArtifactToolName,
			Description: saveChannelArtifactDescription,
			Schema:      llm.NewJSONSchemaFromStruct[SaveChannelArtifactArgs](),
			Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				return t.resolveSave(ctx, llmCtx, argsGetter)
			},
		},
		{
			Name:        RemoveChannelArtifactToolName,
			Description: removeChannelArtifactDescription,
			Schema:      llm.NewJSONSchemaFromStruct[RemoveChannelArtifactArgs](),
			Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				return t.resolveRemove(ctx, llmCtx, argsGetter)
			},
		},
	}
}

func (t *ChannelArtifactTools) audit(event, status string, llmCtx *llm.Context, artifactID string) {
	if t.Audit != nil {
		t.Audit(event, status, llmCtx.RequestingUser.Id, llmCtx.Channel.Id, artifactID)
	}
}

func artifactToolUserMessage(err error) string {
	switch {
	case errors.Is(err, channelartifacts.ErrForbidden), errors.Is(err, channelartifacts.ErrNotFound):
		return "the requesting user may not manage channel artifacts here, or the artifact was not found in this channel"
	case errors.Is(err, channelartifacts.ErrLimitReached):
		return "this channel's artifact library is full; remove one first"
	case errors.Is(err, channelartifacts.ErrDuplicate):
		return "an artifact with identical content is already saved in this channel"
	case errors.Is(err, channelartifacts.ErrTooLarge):
		return "the HTML is too large to save"
	case errors.Is(err, channelartifacts.ErrIneligible):
		return "the file must be an HTML file attached to one of your posts in this channel"
	case errors.Is(err, channelartifacts.ErrInvalidInput):
		return "invalid parameters: check title (1-64 characters) and ids"
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
	userID, channelID := llmCtx.RequestingUser.Id, llmCtx.Channel.Id
	if (args.Content == "") == (args.FileID == "") {
		return "provide exactly one of content or file_id", errors.New("SaveChannelArtifact needs content or file_id")
	}
	if args.ArtifactID != "" && !model.IsValidId(args.ArtifactID) {
		return "artifact_id is not a valid id", errors.New("invalid artifact_id")
	}
	if args.ArtifactID == "" && strings.TrimSpace(args.Title) == "" && args.Content != "" {
		return "title is required for a new artifact", errors.New("SaveChannelArtifact missing title")
	}

	op := channelartifacts.OpAdd
	if args.ArtifactID != "" {
		op = channelartifacts.OpEdit
		entry, err := t.Service.GetReadable(userID, args.ArtifactID)
		if err != nil || entry.ChannelID != channelID {
			return artifactToolUserMessage(channelartifacts.ErrNotFound), errors.New("artifact not in this channel")
		}
	}
	if !t.Service.Can(userID, channelID, op) {
		return artifactToolUserMessage(channelartifacts.ErrForbidden), channelartifacts.ErrForbidden
	}

	limits := t.Limits()
	postID, fileID := "", args.FileID
	if args.Content != "" {
		if int64(len(args.Content)) > limits.MaxSize {
			return artifactToolUserMessage(channelartifacts.ErrTooLarge), channelartifacts.ErrTooLarge
		}
		if policyErr := uploadPolicyAllowed(t.Client, llmCtx); policyErr != nil {
			return createFilePolicyUserMessage(policyErr), fmt.Errorf("SaveChannelArtifact rejected: %w", policyErr)
		}
		var err error
		postID, fileID, err = t.postArtifact(llmCtx, args)
		if err != nil {
			t.audit("saveChannelArtifactByAgent", model.AuditStatusFail, llmCtx, args.ArtifactID)
			return "saving the artifact failed", err
		}
	} else {
		info, err := t.Client.GetFileInfo(fileID)
		if err != nil || info == nil || info.PostId == "" {
			return artifactToolUserMessage(channelartifacts.ErrIneligible), errors.New("file is not attached to a post")
		}
		postID = info.PostId
	}

	entry, err := t.Service.Save(userID, t.Client, channelartifacts.SaveRequest{
		ChannelID: channelID, PostID: postID, FileID: fileID, Title: args.Title, ArtifactID: args.ArtifactID,
	}, limits)
	if err != nil {
		t.audit("saveChannelArtifactByAgent", model.AuditStatusFail, llmCtx, args.ArtifactID)
		return artifactToolUserMessage(err), fmt.Errorf("SaveChannelArtifact failed: %w", err)
	}
	t.audit("saveChannelArtifactByAgent", model.AuditStatusSuccess, llmCtx, entry.ID)
	t.Service.Publish("channel_artifact_updated", channelID, map[string]any{"artifact": entry})

	out, err := json.Marshal(ChannelArtifactResult{
		ArtifactID: entry.ID, Title: entry.Title, Version: entry.Version, PostID: postID,
		Note: "Saved. The library keeps a reference to the message with the file, so the artifact lasts only as long as that message does.",
	})
	if err != nil {
		return "saved", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(out), nil
}

// postArtifact uploads the HTML and posts it to the channel as the agent so a
// real, agent-authored message holds the file the library will reference.
func (t *ChannelArtifactTools) postArtifact(llmCtx *llm.Context, args SaveChannelArtifactArgs) (postID, fileID string, err error) {
	name, nameErr := validateUploadFileName(safeArtifactFileName(args.Title) + ".html")
	if nameErr != nil {
		return "", "", fmt.Errorf("invalid artifact file name: %w", nameErr)
	}
	info, err := t.Client.UploadFile(strings.NewReader(args.Content), name, llmCtx.Channel.Id)
	if err != nil {
		return "", "", fmt.Errorf("upload failed: %w", err)
	}
	post := &model.Post{
		ChannelId: llmCtx.Channel.Id,
		UserId:    llmCtx.BotUserID,
		FileIds:   model.StringArray{info.Id},
	}
	if llmCtx.ResponsePostID != "" {
		if response, getErr := t.Client.GetPost(llmCtx.ResponsePostID); getErr == nil && response != nil {
			post.RootId = response.RootId
		}
	}
	if err := t.Client.CreatePost(post); err != nil {
		return "", "", fmt.Errorf("post failed: %w", err)
	}
	return post.Id, info.Id, nil
}

func safeArtifactFileName(title string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r == '/' || r == '\\' || r < 0x20 || r == 0x7f:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ". ")
	if name == "" {
		return "artifact"
	}
	return name
}

func (t *ChannelArtifactTools) resolveRemove(_ context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
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
	userID, channelID := llmCtx.RequestingUser.Id, llmCtx.Channel.Id
	entry, err := t.Service.GetReadable(userID, args.ArtifactID)
	if err != nil || entry.ChannelID != channelID {
		return artifactToolUserMessage(channelartifacts.ErrNotFound), errors.New("artifact not in this channel")
	}
	if _, err = t.Service.Delete(userID, args.ArtifactID); err != nil {
		t.audit("removeChannelArtifactByAgent", model.AuditStatusFail, llmCtx, args.ArtifactID)
		return artifactToolUserMessage(err), fmt.Errorf("RemoveChannelArtifact failed: %w", err)
	}
	t.audit("removeChannelArtifactByAgent", model.AuditStatusSuccess, llmCtx, args.ArtifactID)
	t.Service.Publish("channel_artifact_deleted", channelID, map[string]any{"id": args.ArtifactID})
	out, err := json.Marshal(ChannelArtifactResult{ArtifactID: args.ArtifactID, Note: "Removed from the library. The original message and file were not deleted."})
	if err != nil {
		return "removed", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(out), nil
}
