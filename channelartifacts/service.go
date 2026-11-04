// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// ChannelMemberLookup loads a channel membership for permission checks.
type ChannelMemberLookup interface {
	GetMember(channelID, userID string) (*model.ChannelMember, error)
}

// Service is the application layer for the channel artifact library.
type Service struct {
	store   *Store
	mm      mmapi.Client
	members ChannelMemberLookup
}

func NewService(store *Store, mm mmapi.Client, members ChannelMemberLookup) *Service {
	return &Service{store: store, mm: mm, members: members}
}

// MembersFromPluginAPI adapts pluginapi.Client to ChannelMemberLookup.
func MembersFromPluginAPI(api *pluginapi.Client) ChannelMemberLookup {
	return channelMembers{api: api}
}

type channelMembers struct {
	api *pluginapi.Client
}

func (c channelMembers) GetMember(channelID, userID string) (*model.ChannelMember, error) {
	return c.api.Channel.GetMember(channelID, userID)
}

// CanManage reports whether userID may mutate the channel's artifact library.
func (s *Service) CanManage(userID, channelID string) bool {
	if userID == "" || channelID == "" {
		return false
	}
	if s.mm.HasPermissionTo(userID, model.PermissionManageSystem) {
		return true
	}
	channel, err := s.mm.GetChannel(channelID)
	if err != nil || channel == nil || channel.DeleteAt != 0 {
		return false
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return s.mm.HasPermissionToChannel(userID, channelID, model.PermissionReadChannel)
	}
	member, err := s.members.GetMember(channelID, userID)
	if err != nil || member == nil {
		return false
	}
	if member.SchemeGuest {
		return false
	}
	return member.SchemeAdmin
}

// CanRead reports whether userID may list/open artifacts in the channel.
func (s *Service) CanRead(userID, channelID string) bool {
	return s.mm.HasPermissionToChannel(userID, channelID, model.PermissionReadChannel)
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
	if entries == nil {
		entries = []*Entry{}
	}
	return &ListResponse{
		Entries:   entries,
		CanManage: s.CanManage(userID, channelID),
	}, nil
}

func (s *Service) GetReadable(userID, id string) (*Entry, error) {
	entry, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !s.CanRead(userID, entry.ChannelID) {
		return nil, ErrNotFound
	}
	return entry, nil
}

func (s *Service) ReadContent(entry *Entry) ([]byte, error) {
	return s.store.ReadContent(entry)
}

func (s *Service) ReadContentAt(entry *Entry, version int) ([]byte, error) {
	return s.store.ReadContentAt(entry, version)
}

func (s *Service) ListVersions(userID, id string) ([]VersionRecord, error) {
	entry, err := s.GetReadable(userID, id)
	if err != nil {
		return nil, err
	}
	return s.store.ListVersions(entry.ID)
}

func defaultTitle(fileName string) string {
	base := fileName
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Artifact"
	}
	if utf8.RuneCountInString(base) > MaxTitleRunes {
		runes := []rune(base)
		base = string(runes[:MaxTitleRunes])
	}
	return base
}

// RootPostID returns post.RootId when set, otherwise post.Id.
func RootPostID(post *model.Post) string {
	if post == nil {
		return ""
	}
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
}

// CreateFromHTML copies HTML bytes into the channel library.
func (s *Service) CreateFromHTML(
	userID string,
	req CreateRequest,
	content []byte,
	fileName string,
	botUserID string,
	agentDisplayName string,
	teamID string,
	sourceRootPostID string,
	limits ContentLimits,
) (*Entry, error) {
	if !s.CanManage(userID, req.ChannelID) {
		return nil, ErrNotFound
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = defaultTitle(fileName)
	}
	now := model.GetMillis()
	if req.ReplaceID != "" {
		return s.replaceLocked(userID, req.ReplaceID, req.ChannelID, req.PostID, req.FileID, fileName, botUserID, agentDisplayName, sourceRootPostID, content, title, strings.TrimSpace(req.Description), limits)
	}
	entry := &Entry{
		ID:                     model.NewId(),
		ChannelID:              req.ChannelID,
		TeamID:                 teamID,
		Title:                  title,
		Description:            strings.TrimSpace(req.Description),
		SourcePostID:           req.PostID,
		SourceFileID:           req.FileID,
		SourceFileName:         fileName,
		SourceBotUserID:        botUserID,
		SourceAgentDisplayName: agentDisplayName,
		SourceRootPostID:       sourceRootPostID,
		CreatedBy:              userID,
		CreateAt:               now,
		UpdateAt:               now,
		UpdatedBy:              userID,
		Version:                1,
	}
	if err := s.store.Save(entry, content, limits.MaxSize, limits.MaxPerChannel, limits.MaxSitewide); err != nil {
		return nil, err
	}
	return entry, nil
}

func (s *Service) replaceLocked(
	userID, replaceID, channelID, postID, fileID, fileName, botUserID, agentDisplayName, sourceRootPostID string,
	content []byte, title, description string, limits ContentLimits,
) (*Entry, error) {
	entry, err := s.store.Get(replaceID)
	if err != nil {
		return nil, err
	}
	if entry.ChannelID != channelID {
		return nil, ErrNotFound
	}
	if !s.CanManage(userID, entry.ChannelID) {
		return nil, ErrNotFound
	}
	if title != "" {
		entry.Title = title
	}
	entry.Description = description
	entry.SourcePostID = postID
	entry.SourceFileID = fileID
	entry.SourceFileName = fileName
	entry.SourceBotUserID = botUserID
	entry.SourceAgentDisplayName = agentDisplayName
	entry.SourceRootPostID = sourceRootPostID
	entry.UpdateAt = model.GetMillis()
	entry.UpdatedBy = userID
	if err := s.store.ReplaceContent(entry, content, limits.MaxSize, limits.VersionsToKeep); err != nil {
		if errors.Is(err, ErrCleanupDeferred) {
			return entry, err
		}
		return nil, err
	}
	return entry, nil
}

// ReplaceContent replaces the HTML of an existing library entry from a new file.
func (s *Service) ReplaceContent(
	userID, id string,
	req ReplaceRequest,
	content []byte,
	fileName string,
	botUserID string,
	agentDisplayName string,
	sourceRootPostID string,
	limits ContentLimits,
) (*Entry, error) {
	entry, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !s.CanManage(userID, entry.ChannelID) {
		return nil, ErrNotFound
	}
	entry.SourcePostID = req.PostID
	entry.SourceFileID = req.FileID
	entry.SourceFileName = fileName
	entry.SourceBotUserID = botUserID
	entry.SourceAgentDisplayName = agentDisplayName
	entry.SourceRootPostID = sourceRootPostID
	entry.UpdateAt = model.GetMillis()
	entry.UpdatedBy = userID
	if err := s.store.ReplaceContent(entry, content, limits.MaxSize, limits.VersionsToKeep); err != nil {
		if errors.Is(err, ErrCleanupDeferred) {
			return entry, err
		}
		return nil, err
	}
	return entry, nil
}

func (s *Service) Patch(userID, id string, req PatchRequest) (*Entry, error) {
	entry, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !s.CanManage(userID, entry.ChannelID) {
		return nil, ErrNotFound
	}
	if req.Title != nil {
		entry.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		entry.Description = strings.TrimSpace(*req.Description)
	}
	entry.UpdateAt = model.GetMillis()
	entry.UpdatedBy = userID
	if err := s.store.UpdateMeta(entry); err != nil {
		return nil, err
	}
	return entry, nil
}

func (s *Service) Rollback(userID, id string, toVersion int) (*Entry, error) {
	entry, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !s.CanManage(userID, entry.ChannelID) {
		return nil, ErrNotFound
	}
	entry.UpdatedBy = userID
	entry.UpdateAt = model.GetMillis()
	if err := s.store.Rollback(entry, toVersion); err != nil {
		return nil, err
	}
	return entry, nil
}

// Delete removes the head version, or purges the artifact when purge is true
// or only one version remains.
func (s *Service) Delete(userID, id string, purge bool) error {
	entry, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if !s.CanManage(userID, entry.ChannelID) {
		return ErrNotFound
	}
	if purge {
		return s.store.Purge(entry)
	}
	versions, err := s.store.ListVersions(entry.ID)
	if err != nil {
		return err
	}
	if len(versions) <= 1 {
		return s.store.Purge(entry)
	}
	return s.store.DeleteHead(entry)
}

func (s *Service) DeleteHead(userID, id string) error {
	return s.Delete(userID, id, false)
}

func (s *Service) Purge(userID, id string) error {
	return s.Delete(userID, id, true)
}

// ListByTeam returns library entries for the team. A system admin sees every
// channel. Anyone else only sees channels they can read.
func (s *Service) ListByTeam(userID, teamID string) (*TeamListResponse, error) {
	if !model.IsValidId(teamID) {
		return nil, ErrInvalidInput
	}
	seeAll := userID != "" && s.mm.HasPermissionTo(userID, model.PermissionManageSystem)
	out := make([]*Entry, 0)
	err := s.store.ForEachChannelIndex(func(channelID string) error {
		if !seeAll && !s.CanRead(userID, channelID) {
			return nil
		}
		entries, err := s.store.List(channelID)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.TeamID == teamID {
				out = append(out, entry)
				continue
			}
			if entry.TeamID == "" {
				channel, err := s.mm.GetChannel(channelID)
				if err != nil || channel == nil {
					continue
				}
				if channel.TeamId == teamID {
					out = append(out, entry)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &TeamListResponse{Entries: out}, nil
}

// PurgeChannel removes every artifact in a channel (deleted-channel cleanup).
// It reads the channel index. List hides an entry whose purge already started,
// and this sweep is what finishes that purge.
func (s *Service) PurgeChannel(channelID string) error {
	if !model.IsValidId(channelID) {
		return ErrInvalidInput
	}
	idx, _, err := s.store.getIndex(channelID)
	if err != nil {
		return err
	}
	for _, id := range idx.IDs {
		entry, getErr := s.store.Get(id)
		if errors.Is(getErr, ErrNotFound) {
			continue
		}
		if getErr != nil {
			return getErr
		}
		if err := s.store.Purge(entry); err != nil {
			return err
		}
	}
	return nil
}

// FindLineageMatch prefers a source_file_id match, else the same SourceRootPostID.
func (s *Service) FindLineageMatch(channelID, postID, fileID, rootID string) *Entry {
	entries, err := s.store.List(channelID)
	if err != nil {
		return nil
	}
	if fileID != "" {
		for _, entry := range entries {
			if entry.SourceFileID == fileID {
				return entry
			}
		}
	}
	matchRoot := rootID
	if matchRoot == "" {
		matchRoot = postID
	}
	if matchRoot == "" {
		return nil
	}
	for _, entry := range entries {
		if entry.SourceRootPostID == matchRoot {
			return entry
		}
	}
	return nil
}

// ExportChannel returns every artifact in a channel with all version bodies.
func (s *Service) ExportChannel(channelID string) (*ExportResponse, error) {
	if !model.IsValidId(channelID) {
		return nil, ErrInvalidInput
	}
	entries, err := s.store.List(channelID)
	if err != nil {
		return nil, err
	}
	out := &ExportResponse{Entries: make([]ExportEntry, 0, len(entries))}
	for _, entry := range entries {
		versions, err := s.store.ListVersions(entry.ID)
		if err != nil {
			return nil, err
		}
		bodies := make(map[string]string, len(versions))
		for _, rec := range versions {
			body, err := s.store.ReadContentAt(entry, rec.Version)
			if err != nil {
				return nil, err
			}
			bodies[VersionKeyString(rec.Version)] = base64.StdEncoding.EncodeToString(body)
		}
		out.Entries = append(out.Entries, ExportEntry{
			Meta:                   entry,
			Versions:               versions,
			ContentBase64ByVersion: bodies,
		})
	}
	return out, nil
}

func (s *Service) Reorder(userID string, req OrderRequest) error {
	if !s.CanManage(userID, req.ChannelID) {
		return ErrNotFound
	}
	return s.store.Reorder(req.ChannelID, req.IDs)
}

// Store exposes the underlying store for retention sweeps.
func (s *Service) Store() *Store {
	return s.store
}

// DuplicateID extracts the existing artifact id from an ErrDuplicate wrapped error.
func DuplicateID(err error) string {
	if err == nil || !errors.Is(err, ErrDuplicate) {
		return ""
	}
	const prefix = "identical artifact already saved: "
	_, rest, ok := strings.Cut(err.Error(), prefix)
	if ok {
		return strings.TrimSpace(rest)
	}
	return ""
}

// Publish broadcasts a channel-scoped websocket event. Event names are short;
// the plugin ID is prefixed by the server. Payloads are JSON-round-tripped so
// only gob-safe values cross the plugin RPC boundary (structs in map[string]any
// crash the plugin process).
func (s *Service) Publish(event string, channelID string, payload map[string]any) {
	safe := map[string]any{}
	if payload != nil {
		if raw, err := json.Marshal(payload); err == nil {
			_ = json.Unmarshal(raw, &safe)
		}
	}
	if safe == nil {
		safe = map[string]any{}
	}
	safe["channel_id"] = channelID
	safe["at"] = time.Now().UnixMilli()
	s.mm.PublishWebSocketEvent(event, safe, &model.WebsocketBroadcast{ChannelId: channelID})
}
