// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"context"
)

// Actor is who a library call acts as.
type Actor struct {
	// UserID is whose channel and bookmark permissions apply.
	UserID string
	// BotUserID is the agent that authored content written by an agent. It is
	// required to write content (WriteRequest.Body) and ignored otherwise.
	BotUserID string
	// SessionID is the session whose file download policy applies when a
	// Mattermost file is read or referenced. Unattended callers, such as wake
	// turns and automations, have none: they can write content but not read
	// files back.
	SessionID string
}

// WriteRequest saves an artifact, or a new version of one. Exactly one source
// is given: PostID and FileID name a file already attached to an agent's post
// in the channel (which needs Actor.SessionID), or Body is content the library
// publishes to the channel as Actor.BotUserID and then references.
type WriteRequest struct {
	SaveRequest

	// Body is the complete content. It never comes from an HTTP request.
	Body []byte
	// ThreadOfPostID is optional: the message that holds Body is posted in the
	// same thread as this post.
	ThreadOfPostID string
}

// ReadOptions selects what Read returns.
type ReadOptions struct {
	// Version is the version to read; 0 means the current version.
	Version int
	// MetadataOnly returns the entry and version record without reading the
	// content, so it needs no session and checks no file policy.
	MetadataOnly bool
}

// Content is one version of an artifact.
type Content struct {
	// Entry describes the artifact as of Version: its number, hash, size and
	// source are that version's, not the current one's.
	Entry *Entry
	// Version is the stored record of the version that was read.
	Version *VersionRecord
	// Body is the content; empty for MetadataOnly reads.
	Body []byte
}

// Library is a channel's collection of saved artifacts. The HTTP API, the agent
// tools and the MCP read tools depend on this interface only, so a different
// storage and listing backend can replace the post-backed one.
//
// Every method authorizes the actor, and fails closed. Errors are the fixed
// Err* values of this package, possibly wrapped (DuplicateError for
// ErrDuplicate); a backend never returns another error for an expected
// outcome. Implementations publish the Event* websocket events for changes.
type Library interface {
	// List returns the channel's entries and what the actor may do with them.
	// It fails with ErrNotFound when the actor cannot read the channel.
	List(ctx context.Context, actor Actor, channelID string) (*ListResponse, error)
	// Get returns one entry the actor may read, or ErrNotFound.
	Get(ctx context.Context, actor Actor, id string) (*Entry, error)
	// Read returns one version's content. It fails with ErrSessionRequired
	// without Actor.SessionID, ErrUnavailable when the source is gone or has
	// changed, and ErrNotFound when the file policy denies the session.
	Read(ctx context.Context, actor Actor, id string, opts ReadOptions) (*Content, error)
	// Versions lists the retained versions, newest first.
	Versions(ctx context.Context, actor Actor, id string) ([]VersionRecord, error)
	// Write saves content as a new artifact or as the next version of
	// WriteRequest.ArtifactID and returns the resulting entry. A saved artifact
	// keeps its format. It fails with ErrDuplicate when the channel already has
	// that content as an artifact's current version, ErrLimitReached when the
	// channel is full, and ErrTooLarge beyond the size limit.
	Write(ctx context.Context, actor Actor, req WriteRequest) (*Entry, error)
	// Remove deletes the artifact and its versions and returns the removed
	// entry. The source messages and files stay.
	Remove(ctx context.Context, actor Actor, id string) (*Entry, error)
}

// Manager is a Library that also supports the curation the web UI offers.
type Manager interface {
	Library
	// Rename changes an artifact's title.
	Rename(ctx context.Context, actor Actor, id, title string) (*Entry, error)
	// Rollback makes an earlier version current, or fails with ErrUnavailable
	// when that version's source is gone.
	Rollback(ctx context.Context, actor Actor, id string, version int) (*Entry, error)
	// RemoveVersion deletes one version; the last remaining one cannot be.
	RemoveVersion(ctx context.Context, actor Actor, id string, version int) (*Entry, error)
	// CheckReadable reports whether the actor's session could read the version
	// now, without reading it. version 0 means the current version.
	CheckReadable(ctx context.Context, actor Actor, id string, version int) error
}

// EntryAt describes entry as it was at one version, so metadata returned beside
// a version's content is that version's.
func EntryAt(entry *Entry, rec *VersionRecord) *Entry {
	at := *entry
	at.Version = rec.Version
	at.SHA256 = rec.SHA256
	at.Size = rec.Size
	at.SourcePostID = rec.SourcePostID
	at.SourceFileID = rec.SourceFileID
	at.SourceBotUserID = rec.SourceBotUserID
	at.SourceAgentDisplayName = rec.SourceAgentDisplayName
	at.Available = rec.Available
	return &at
}

type postLibrary struct {
	svc    *Service
	limits func() Limits
}

// NewLibrary returns the Library backed by the service's SQL metadata and the
// Mattermost posts and files it references. limits is read on every call so
// site config changes apply at once.
func NewLibrary(svc *Service, limits func() Limits) Manager {
	return &postLibrary{svc: svc, limits: limits}
}

func (l *postLibrary) List(_ context.Context, actor Actor, channelID string) (*ListResponse, error) {
	resp, err := l.svc.List(actor.UserID, channelID)
	if err != nil {
		return nil, err
	}
	resp.MaxPerChannel = resolveMax(l.limits().MaxPerChannel, DefaultMaxPerChannel)
	return resp, nil
}

func (l *postLibrary) Get(_ context.Context, actor Actor, id string) (*Entry, error) {
	return l.svc.GetReadable(actor.UserID, id)
}

func (l *postLibrary) Versions(_ context.Context, actor Actor, id string) ([]VersionRecord, error) {
	return l.svc.Versions(actor.UserID, id)
}

func (l *postLibrary) Read(_ context.Context, actor Actor, id string, opts ReadOptions) (*Content, error) {
	if opts.Version < 0 {
		return nil, ErrInvalidInput
	}
	if !opts.MetadataOnly && actor.SessionID == "" {
		return nil, ErrSessionRequired
	}
	entry, err := l.svc.GetReadable(actor.UserID, id)
	if err != nil {
		return nil, err
	}
	rec, err := l.svc.VersionOf(entry, opts.Version)
	if err != nil {
		return nil, err
	}
	out := &Content{Entry: EntryAt(entry, rec), Version: rec}
	if opts.MetadataOnly {
		return out, nil
	}
	out.Body, err = l.svc.ReadContent(actor.SessionID, entry, opts.Version, l.limits().MaxSize)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (l *postLibrary) Write(_ context.Context, actor Actor, req WriteRequest) (*Entry, error) {
	var (
		entry *Entry
		err   error
	)
	switch {
	case len(req.Body) > 0 && (req.PostID != "" || req.FileID != ""):
		return nil, ErrInvalidInput
	case len(req.Body) > 0:
		entry, err = l.svc.SaveBody(actor, req, l.limits())
	case actor.SessionID == "":
		return nil, ErrSessionRequired
	default:
		entry, err = l.svc.SaveFile(actor, req.SaveRequest, l.limits())
	}
	if err != nil {
		return nil, err
	}
	event := EventCreated
	if req.ArtifactID != "" {
		event = EventUpdated
	}
	l.svc.Publish(event, entry.ChannelID, map[string]any{"artifact": entry})
	return entry, nil
}

func (l *postLibrary) Remove(_ context.Context, actor Actor, id string) (*Entry, error) {
	entry, err := l.svc.Delete(actor.UserID, id)
	if err != nil {
		return nil, err
	}
	l.svc.Publish(EventDeleted, entry.ChannelID, map[string]any{"id": id})
	return entry, nil
}

func (l *postLibrary) Rename(_ context.Context, actor Actor, id, title string) (*Entry, error) {
	entry, err := l.svc.Patch(actor.UserID, id, PatchRequest{Title: title})
	if err != nil {
		return nil, err
	}
	l.svc.Publish(EventUpdated, entry.ChannelID, map[string]any{"artifact": entry})
	return entry, nil
}

func (l *postLibrary) Rollback(_ context.Context, actor Actor, id string, version int) (*Entry, error) {
	entry, err := l.svc.Rollback(actor.UserID, id, version)
	if err != nil {
		return nil, err
	}
	l.svc.Publish(EventUpdated, entry.ChannelID, map[string]any{"artifact": entry})
	return entry, nil
}

func (l *postLibrary) RemoveVersion(_ context.Context, actor Actor, id string, version int) (*Entry, error) {
	entry, err := l.svc.DeleteVersion(actor.UserID, id, version)
	if err != nil {
		return nil, err
	}
	l.svc.Publish(EventUpdated, entry.ChannelID, map[string]any{"artifact": entry})
	return entry, nil
}

func (l *postLibrary) CheckReadable(_ context.Context, actor Actor, id string, version int) error {
	if actor.SessionID == "" {
		return ErrSessionRequired
	}
	entry, err := l.svc.GetReadable(actor.UserID, id)
	if err != nil {
		return err
	}
	return l.svc.CheckReadable(actor.SessionID, entry, version)
}
