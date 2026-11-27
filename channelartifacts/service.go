// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// BotDirectory identifies agents. Only files posted by an agent can be saved.
type BotDirectory interface {
	IsAnyBot(userID string) bool
	// AgentDisplayName returns the agent's current display name, or "" when the
	// user is not a known agent.
	AgentDisplayName(userID string) string
}

// Op is a kind of change to a channel's library. Each maps to the channel
// bookmark permission of the same name. Libraries exist only in public and
// private channels, never in direct or group messages.
type Op int

const (
	OpAdd Op = iota
	OpEdit
	OpDelete
)

// Service applies the library rules: who may read and change a channel's
// library, which files may be saved, and how a saved version is read back.
type Service struct {
	store *Store
	mm    mmapi.Client
	bots  BotDirectory
}

func NewService(store *Store, mm mmapi.Client, bots BotDirectory) *Service {
	return &Service{store: store, mm: mm, bots: bots}
}

func permissionFor(op Op, channelType model.ChannelType) *model.Permission {
	private := channelType == model.ChannelTypePrivate
	switch op {
	case OpAdd:
		if private {
			return model.PermissionAddBookmarkPrivateChannel
		}
		return model.PermissionAddBookmarkPublicChannel
	case OpEdit:
		if private {
			return model.PermissionEditBookmarkPrivateChannel
		}
		return model.PermissionEditBookmarkPublicChannel
	default:
		if private {
			return model.PermissionDeleteBookmarkPrivateChannel
		}
		return model.PermissionDeleteBookmarkPublicChannel
	}
}

// libraryChannel returns the channel when it can hold a library. Only public
// and private channels can; direct and group messages cannot, and neither can a
// channel that doesn't exist or whose lookup fails.
func (s *Service) libraryChannel(channelID string) (*model.Channel, bool) {
	if !model.IsValidId(channelID) {
		return nil, false
	}
	channel, err := s.mm.GetChannel(channelID)
	if err != nil || channel == nil {
		return nil, false
	}
	if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
		return nil, false
	}
	return channel, true
}

// SupportsChannelType reports whether a channel of this type can hold a library.
func SupportsChannelType(channelType model.ChannelType) bool {
	return channelType == model.ChannelTypeOpen || channelType == model.ChannelTypePrivate
}

// Can reports whether userID may make the change in the channel. It follows the
// permissions core channel bookmarks use, and checks them only after the
// channel is known to be a public or private channel that isn't archived.
func (s *Service) Can(userID, channelID string, op Op) bool {
	if userID == "" {
		return false
	}
	channel, ok := s.libraryChannel(channelID)
	if !ok || channel.DeleteAt != 0 {
		return false
	}
	return s.mm.HasPermissionToChannel(userID, channelID, permissionFor(op, channel.Type))
}

// CanRead reports whether userID may list and open the channel's library.
// Direct and group messages have none.
func (s *Service) CanRead(userID, channelID string) bool {
	if userID == "" {
		return false
	}
	if _, ok := s.libraryChannel(channelID); !ok {
		return false
	}
	return s.mm.HasPermissionToChannel(userID, channelID, model.PermissionReadChannel)
}

// authorize returns ErrNotFound to callers who cannot read the channel, so a
// library's existence stays hidden, and ErrForbidden to readers who may not
// make this change.
func (s *Service) authorize(userID, channelID string, op Op) error {
	if !s.CanRead(userID, channelID) {
		return ErrNotFound
	}
	if !s.Can(userID, channelID, op) {
		return ErrForbidden
	}
	return nil
}

func (s *Service) decorateEntry(entry *Entry) {
	if s.bots != nil {
		entry.SourceAgentDisplayName = s.bots.AgentDisplayName(entry.SourceBotUserID)
	}
}

func (s *Service) List(userID, channelID string) (*ListResponse, error) {
	if !model.IsValidId(channelID) {
		return nil, ErrInvalidInput
	}
	if !s.CanRead(userID, channelID) {
		return nil, ErrNotFound
	}
	entries, err := s.store.List(channelID)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		s.decorateEntry(entry)
	}
	return &ListResponse{
		Entries:   entries,
		CanAdd:    s.Can(userID, channelID, OpAdd),
		CanEdit:   s.Can(userID, channelID, OpEdit),
		CanDelete: s.Can(userID, channelID, OpDelete),
	}, nil
}

// GetReadable returns the entry when userID may read its channel.
func (s *Service) GetReadable(userID, id string) (*Entry, error) {
	if !model.IsValidId(id) {
		return nil, ErrInvalidInput
	}
	entry, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !s.CanRead(userID, entry.ChannelID) {
		return nil, ErrNotFound
	}
	s.decorateEntry(entry)
	return entry, nil
}

func (s *Service) Versions(userID, id string) ([]VersionRecord, error) {
	entry, err := s.GetReadable(userID, id)
	if err != nil {
		return nil, err
	}
	versions, err := s.store.Versions(entry.ID)
	if err != nil {
		return nil, err
	}
	for i := range versions {
		if s.bots != nil {
			versions[i].SourceAgentDisplayName = s.bots.AgentDisplayName(versions[i].SourceBotUserID)
		}
	}
	return versions, nil
}

// VersionOf returns one version of an entry the caller may already read, as
// stored: who saved it and when. version 0 means the current version.
func (s *Service) VersionOf(entry *Entry, version int) (*VersionRecord, error) {
	if version == 0 {
		version = entry.Version
	}
	rec, err := s.store.Version(entry.ID, version)
	if err != nil {
		return nil, err
	}
	if s.bots != nil {
		rec.SourceAgentDisplayName = s.bots.AgentDisplayName(rec.SourceBotUserID)
	}
	return rec, nil
}

// sourceOf is VersionOf for what reading needs: where the version's content
// lives and its hash. The current version comes from the entry without a query.
func (s *Service) sourceOf(entry *Entry, version int) (*VersionRecord, error) {
	if version == 0 || version == entry.Version {
		return &VersionRecord{
			Version: entry.Version, SHA256: entry.SHA256, Size: entry.Size,
			SourcePostID: entry.SourcePostID, SourceFileID: entry.SourceFileID,
			SourceBotUserID: entry.SourceBotUserID, Head: true, Available: entry.Available,
		}, nil
	}
	return s.store.Version(entry.ID, version)
}

// IsHTMLFile reports whether the file is an HTML document by extension.
func IsHTMLFile(info *model.FileInfo) bool {
	ext := strings.ToLower(strings.TrimPrefix(info.Extension, "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(info.Name), "."))
	}
	return ext == "html" || ext == "htm"
}

func defaultTitle(fileName string) string {
	base := strings.TrimSpace(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
	if base == "" {
		base = "Artifact"
	}
	if utf8.RuneCountInString(base) > MaxTitleRunes {
		base = string([]rune(base)[:MaxTitleRunes])
	}
	return base
}

func validateTitle(title string) error {
	if n := utf8.RuneCountInString(title); n == 0 || n > MaxTitleRunes {
		return ErrInvalidInput
	}
	return nil
}

type source struct {
	sha256   string
	size     int64
	botID    string
	fileName string
}

// readLimited reads at most limit bytes and fails with ErrTooLarge beyond that.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, ErrTooLarge
	}
	return body, nil
}

func hashHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (s *Service) openFile(reader mmapi.Client, fileID string) (io.ReadCloser, error) {
	rc, err := reader.GetFile(fileID)
	switch {
	case err == nil:
		return rc, nil
	case errors.Is(err, mmapi.ErrFileActionForbidden):
		return nil, ErrNotFound
	case mmapi.IsNotFound(err):
		return nil, ErrUnavailable
	default:
		return nil, err
	}
}

// checkSource verifies the file is an HTML attachment of an agent's post in the
// channel and records its SHA-256. reader decides whose file policy applies.
func (s *Service) checkSource(reader mmapi.Client, channelID, postID, fileID string, maxSize int64) (*source, error) {
	if !model.IsValidId(postID) || !model.IsValidId(fileID) {
		return nil, ErrInvalidInput
	}
	info, err := reader.GetFileInfo(fileID)
	switch {
	case err == nil:
	case errors.Is(err, mmapi.ErrFileActionForbidden), mmapi.IsNotFound(err):
		return nil, ErrNotFound
	default:
		return nil, err
	}
	if info == nil || info.DeleteAt != 0 {
		return nil, ErrNotFound
	}
	if !IsHTMLFile(info) || info.PostId != postID {
		return nil, ErrIneligible
	}
	post, err := s.mm.GetPost(postID)
	switch {
	case err == nil:
	case mmapi.IsNotFound(err):
		return nil, ErrNotFound
	default:
		return nil, err
	}
	if post == nil || post.DeleteAt != 0 {
		return nil, ErrNotFound
	}
	if post.ChannelId != channelID || !slices.Contains(post.FileIds, fileID) {
		return nil, ErrIneligible
	}
	if s.bots == nil || !s.bots.IsAnyBot(post.UserId) {
		return nil, ErrIneligible
	}
	if info.Size > maxSize {
		return nil, ErrTooLarge
	}
	rc, err := s.openFile(reader, fileID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	body, err := readLimited(rc, maxSize)
	if err != nil {
		return nil, err
	}
	return &source{sha256: hashHex(body), size: int64(len(body)), botID: post.UserId, fileName: info.Name}, nil
}

// Save registers the file as a new artifact in req.ChannelID, or as a new
// current version of req.ArtifactID. Only a reference to the post and file is
// stored. reader supplies the file policy the content is read under.
func (s *Service) Save(userID string, reader mmapi.Client, req SaveRequest, lim Limits) (*Entry, error) {
	if !model.IsValidId(req.ChannelID) || (req.ArtifactID != "" && !model.IsValidId(req.ArtifactID)) {
		return nil, ErrInvalidInput
	}
	if req.ArtifactID != "" {
		existing, err := s.store.Get(req.ArtifactID)
		if err != nil {
			return nil, err
		}
		if existing.ChannelID != req.ChannelID {
			return nil, ErrNotFound
		}
	}
	op := OpAdd
	if req.ArtifactID != "" {
		op = OpEdit
	}
	if err := s.authorize(userID, req.ChannelID, op); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if title != "" {
		if err := validateTitle(title); err != nil {
			return nil, err
		}
	}
	src, err := s.checkSource(reader, req.ChannelID, req.PostID, req.FileID, lim.MaxSize)
	if err != nil {
		return nil, err
	}
	version := NewVersion{
		PostID: req.PostID, FileID: req.FileID, SHA256: src.sha256, Size: src.size,
		SourceBotUserID: src.botID, UserID: userID, At: model.GetMillis(), Title: title,
	}
	id := req.ArtifactID
	if id == "" {
		id = model.NewId()
		if title == "" {
			title = defaultTitle(src.fileName)
		}
		err = s.store.Create(id, req.ChannelID, title, version, resolveMax(lim.MaxPerChannel, DefaultMaxPerChannel))
	} else {
		err = s.store.AddVersion(id, version, resolveMax(lim.VersionsToKeep, DefaultVersionsToKeep))
	}
	if err != nil {
		return nil, err
	}
	return s.GetReadable(userID, id)
}

func resolveMax(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func (s *Service) Patch(userID, id string, req PatchRequest) (*Entry, error) {
	entry, err := s.GetReadable(userID, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(userID, entry.ChannelID, OpEdit); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	if err := s.store.Rename(id, title, userID, model.GetMillis()); err != nil {
		return nil, err
	}
	return s.GetReadable(userID, id)
}

// Rollback makes an earlier version current. It fails with ErrUnavailable when
// that version's source is gone.
func (s *Service) Rollback(userID, id string, version int) (*Entry, error) {
	entry, err := s.GetReadable(userID, id)
	if err != nil {
		return nil, err
	}
	if err = s.authorize(userID, entry.ChannelID, OpEdit); err != nil {
		return nil, err
	}
	rec, err := s.store.Version(id, version)
	if err != nil {
		return nil, err
	}
	if !rec.Available {
		return nil, ErrUnavailable
	}
	if err := s.store.SetHead(id, version, userID, model.GetMillis()); err != nil {
		return nil, err
	}
	return s.GetReadable(userID, id)
}

// DeleteVersion removes one version's row. The source post and file are not
// touched.
func (s *Service) DeleteVersion(userID, id string, version int) (*Entry, error) {
	entry, err := s.GetReadable(userID, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(userID, entry.ChannelID, OpDelete); err != nil {
		return nil, err
	}
	if err := s.store.DeleteVersion(id, version, userID, model.GetMillis()); err != nil {
		return nil, err
	}
	return s.GetReadable(userID, id)
}

// Delete removes the artifact and its version rows. The source posts and files
// are not touched.
func (s *Service) Delete(userID, id string) (*Entry, error) {
	entry, err := s.GetReadable(userID, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(userID, entry.ChannelID, OpDelete); err != nil {
		return nil, err
	}
	if err := s.store.Purge(id); err != nil {
		return nil, err
	}
	return entry, nil
}

// ReadContent reads one version's HTML from its source file under sessionID's
// file download policy. version 0 means the current version. It fails closed:
// a denied policy is ErrNotFound, a deleted source or changed content is
// ErrUnavailable.
func (s *Service) ReadContent(sessionID string, entry *Entry, version int, maxSize int64) ([]byte, error) {
	rec, err := s.sourceOf(entry, version)
	if err != nil {
		return nil, err
	}
	if !rec.Available {
		return nil, ErrUnavailable
	}
	rc, err := s.openFile(mmapi.WithFilePolicy(s.mm, sessionID), rec.SourceFileID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	body, err := readLimited(rc, maxSize)
	if err != nil {
		return nil, err
	}
	if hashHex(body) != rec.SHA256 {
		return nil, ErrUnavailable
	}
	return body, nil
}

// CheckReadable reports whether the session could read the version's content
// now, without reading it: the same file download policy and availability
// checks as ReadContent. version 0 means the current version.
func (s *Service) CheckReadable(sessionID string, entry *Entry, version int) error {
	rec, err := s.sourceOf(entry, version)
	if err != nil {
		return err
	}
	if !rec.Available {
		return ErrUnavailable
	}
	info, err := mmapi.WithFilePolicy(s.mm, sessionID).GetFileInfo(rec.SourceFileID)
	switch {
	case err == nil:
	case errors.Is(err, mmapi.ErrFileActionForbidden):
		return ErrNotFound
	case mmapi.IsNotFound(err):
		return ErrUnavailable
	default:
		return err
	}
	if info == nil || info.DeleteAt != 0 {
		return ErrUnavailable
	}
	return nil
}

// SweepDeletedChannels removes the rows of artifacts in permanently deleted
// channels. Content is never deleted by the library.
func (s *Service) SweepDeletedChannels() (int64, error) {
	return s.store.SweepDeletedChannels()
}

// Publish broadcasts a channel-scoped websocket event. Event names are short;
// the plugin ID is prefixed by the server. Payloads are JSON-round-tripped so
// only gob-safe values cross the plugin RPC boundary (structs in map[string]any
// crash the plugin process).
func (s *Service) Publish(event, channelID string, payload map[string]any) {
	safe := map[string]any{}
	if raw, err := json.Marshal(payload); err == nil {
		_ = json.Unmarshal(raw, &safe)
	}
	if safe == nil {
		safe = map[string]any{}
	}
	safe["channel_id"] = channelID
	safe["at"] = time.Now().UnixMilli()
	s.mm.PublishWebSocketEvent(event, safe, &model.WebsocketBroadcast{ChannelId: channelID})
}
