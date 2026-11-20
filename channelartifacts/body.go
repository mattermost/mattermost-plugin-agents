// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const fallbackFileName = "artifact"

// SaveFile saves a file already attached to an agent's post, read under the
// actor's session file policy.
func (s *Service) SaveFile(actor Actor, req SaveRequest, lim Limits) (*Entry, error) {
	if actor.SessionID == "" {
		return nil, ErrSessionRequired
	}
	return s.Save(actor.UserID, mmapi.WithFilePolicy(s.mm, actor.SessionID), req, lim)
}

// SaveBody publishes req.Body to the channel as a message from actor.BotUserID,
// with the content as an attached file, and saves that message as the
// artifact's source. A message the library then refuses to record is removed.
//
// The caller needs no session: the content is the agent's own, not a file read
// from Mattermost. When the actor does have one, the saved file is read back
// under its file download policy so a user who couldn't download it never
// gets it registered.
func (s *Service) SaveBody(actor Actor, req WriteRequest, lim Limits) (*Entry, error) {
	body := req.Body
	if len(body) == 0 || !utf8.Valid(body) {
		return nil, ErrInvalidInput
	}
	if int64(len(body)) > lim.MaxSize {
		return nil, ErrTooLarge
	}
	title, format, err := s.authorizeWrite(actor.UserID, req.SaveRequest)
	if err != nil {
		return nil, err
	}
	if s.bots == nil || !s.bots.IsAnyBot(actor.BotUserID) {
		return nil, ErrForbidden
	}
	if format == "" {
		format = FormatHTML
	}
	if title == "" && req.ArtifactID == "" {
		return nil, ErrInvalidInput
	}
	if err = s.checkUpload(actor.UserID, req.ChannelID); err != nil {
		return nil, err
	}

	info, err := s.mm.UploadFile(bytes.NewReader(body), fileNameFor(title, format), req.ChannelID)
	if err != nil {
		return nil, err
	}
	post := &model.Post{
		ChannelId: req.ChannelID,
		UserId:    actor.BotUserID,
		FileIds:   model.StringArray{info.Id},
	}
	if req.ThreadOfPostID != "" {
		if sibling, getErr := s.mm.GetPost(req.ThreadOfPostID); getErr == nil && sibling != nil {
			post.RootId = sibling.RootId
			if post.RootId == "" {
				post.RootId = sibling.Id
			}
		}
	}
	if err = s.mm.CreatePost(post); err != nil {
		return nil, err
	}

	saved := req.SaveRequest
	saved.PostID, saved.FileID = post.Id, info.Id
	entry, err := s.saveUploaded(actor, saved, title, format, body, lim)
	if err != nil {
		if delErr := s.mm.DeletePost(post.Id); delErr != nil {
			s.mm.LogError("Failed to remove the message of a channel artifact that was not saved", "error", delErr, "post_id", post.Id)
		}
		return nil, err
	}
	return entry, nil
}

func (s *Service) saveUploaded(actor Actor, req SaveRequest, title string, format Format, body []byte, lim Limits) (*Entry, error) {
	if actor.SessionID != "" {
		src, err := s.checkSource(mmapi.WithFilePolicy(s.mm, actor.SessionID), req.ChannelID, req.PostID, req.FileID, format, lim.MaxSize)
		if err != nil {
			return nil, err
		}
		return s.persist(actor.UserID, req, title, src, lim)
	}
	src := &source{postID: req.PostID, sha256: hashHex(body), size: int64(len(body)), botID: actor.BotUserID, fileName: fileNameFor(title, format), format: format}
	return s.persist(actor.UserID, req, title, src, lim)
}

// checkUpload fails closed unless the server allows attachments and userID may
// upload to the channel, the same gate the message composer applies.
func (s *Service) checkUpload(userID, channelID string) error {
	cfg := s.mm.GetConfig()
	if cfg != nil && cfg.FileSettings.EnableFileAttachments != nil && !*cfg.FileSettings.EnableFileAttachments {
		return ErrForbidden
	}
	if !s.mm.HasPermissionToChannel(userID, channelID, model.PermissionUploadFile) {
		return ErrForbidden
	}
	return nil
}

// fileNameFor builds the attachment name from the title, with path separators
// and control characters replaced.
func fileNameFor(title string, format Format) string {
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
		name = fallbackFileName
	}
	return name + format.Extension()
}
