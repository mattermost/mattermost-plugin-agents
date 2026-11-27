// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"errors"
	"fmt"
)

const (
	// DefaultMaxPerChannel caps how many artifacts a channel may keep when
	// the site config leaves the limit unset (0).
	DefaultMaxPerChannel = 50

	// DefaultVersionsToKeep is how many versions of one artifact are kept when
	// the site config leaves the setting unset (0).
	DefaultVersionsToKeep = 20

	// MaxTitleRunes matches core channel bookmark display name limits.
	MaxTitleRunes = 64
)

var (
	ErrNotFound     = errors.New("channel artifact not found")
	ErrForbidden    = errors.New("not allowed to manage channel artifacts in this channel")
	ErrLimitReached = errors.New("channel artifact limit reached")
	ErrInvalidInput = errors.New("invalid channel artifact input")
	ErrTooLarge     = errors.New("channel artifact too large")
	// ErrUnavailable means the source post or file of a version is gone, or its
	// bytes no longer match what was recorded when it was saved.
	ErrUnavailable = errors.New("channel artifact source is unavailable")
	// ErrIneligible means the file cannot be saved: not an HTML file attached
	// to an agent's post in the channel.
	ErrIneligible = errors.New("file cannot be saved as a channel artifact")
	// ErrDuplicate is wrapped by DuplicateError.
	ErrDuplicate = errors.New("identical artifact already saved")
)

// DuplicateError reports the artifact whose current version already has the
// content being saved.
type DuplicateError struct {
	ArtifactID string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("%s: %s", ErrDuplicate, e.ArtifactID)
}

func (e *DuplicateError) Is(target error) bool { return target == ErrDuplicate }

// Entry is one saved artifact together with its current version. The library
// stores references to the source post and file, never a copy of the content.
type Entry struct {
	ID        string `json:"id" db:"id"`
	ChannelID string `json:"channel_id" db:"channelid"`
	Title     string `json:"title" db:"title"`
	// Version is the current (head) version number.
	Version                int    `json:"version" db:"version"`
	SHA256                 string `json:"sha256" db:"sha256"`
	Size                   int64  `json:"size" db:"size"`
	SourcePostID           string `json:"source_post_id" db:"sourcepostid"`
	SourceFileID           string `json:"source_file_id" db:"sourcefileid"`
	SourceBotUserID        string `json:"source_bot_user_id" db:"sourcebotuserid"`
	SourceAgentDisplayName string `json:"source_agent_display_name,omitempty" db:"-"`
	CreatedBy              string `json:"created_by" db:"createdby"`
	CreateAt               int64  `json:"create_at" db:"createat"`
	UpdatedBy              string `json:"updated_by" db:"updatedby"`
	UpdateAt               int64  `json:"update_at" db:"updateat"`
	// Available is false when the current version's source post or file was
	// deleted. The entry stays listed so the loss is visible.
	Available bool `json:"available" db:"available"`
}

// VersionRecord is one saved version of an artifact.
type VersionRecord struct {
	Version                int    `json:"version" db:"version"`
	SHA256                 string `json:"sha256" db:"sha256"`
	Size                   int64  `json:"size" db:"size"`
	SourcePostID           string `json:"source_post_id" db:"sourcepostid"`
	SourceFileID           string `json:"source_file_id" db:"sourcefileid"`
	SourceBotUserID        string `json:"source_bot_user_id" db:"sourcebotuserid"`
	SourceAgentDisplayName string `json:"source_agent_display_name,omitempty" db:"-"`
	CreatedBy              string `json:"created_by" db:"createdby"`
	CreateAt               int64  `json:"create_at" db:"createat"`
	Head                   bool   `json:"head" db:"head"`
	Available              bool   `json:"available" db:"available"`
}

// ListResponse is the channel library plus what the caller may do with it.
type ListResponse struct {
	Entries       []*Entry `json:"entries"`
	CanAdd        bool     `json:"can_add"`
	CanEdit       bool     `json:"can_edit"`
	CanDelete     bool     `json:"can_delete"`
	MaxPerChannel int      `json:"max_per_channel"`
}

// Limits carries the size and retention caps into writes.
type Limits struct {
	// MaxSize caps the HTML size in bytes.
	MaxSize        int64
	MaxPerChannel  int
	VersionsToKeep int
}

// SaveRequest registers a source file as a new artifact, or as a new version of
// ArtifactID when it is set.
type SaveRequest struct {
	ChannelID  string `json:"channel_id"`
	PostID     string `json:"post_id"`
	FileID     string `json:"file_id"`
	Title      string `json:"title"`
	ArtifactID string `json:"replace_id,omitempty"`
}

// PatchRequest is the body for renaming a saved artifact.
type PatchRequest struct {
	Title string `json:"title"`
}

// RollbackRequest selects which version becomes current.
type RollbackRequest struct {
	Version int `json:"version"`
}
