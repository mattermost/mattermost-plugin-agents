// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package autoreply

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

// Mode is the per-channel auto-reply mode. The zero value is not valid.
type Mode string

const (
	// ModeOff disables auto-reply. It is never stored: "off" is represented by
	// the absence of a row, so ModeOff exists only as an API value (Phase 2 maps
	// a PUT with mode "off" to Service.Delete).
	ModeOff Mode = "off"
	// ModeRootPosts auto-replies to new root posts only.
	ModeRootPosts Mode = "root_posts"
	// ModeThreads auto-replies to root posts and to non-bot thread replies.
	ModeThreads Mode = "threads"
	// ModeAmbient classifies each eligible post and replies only when the
	// classifier returns should_reply=true.
	ModeAmbient Mode = "ambient"
)

// MaxInstructionsRunes is the stored/API cap for Setting.Instructions.
const MaxInstructionsRunes = 8000

// MaxAnalysisModelLen is the stored/API cap for Setting.AnalysisModel in both
// bytes and runes (VARCHAR(512); model IDs are ASCII).
const MaxAnalysisModelLen = 512

// IsStorable reports whether the mode is one that may be persisted.
func (m Mode) IsStorable() bool {
	return m == ModeRootPosts || m == ModeThreads || m == ModeAmbient
}

// Setting is one channel's auto-reply configuration. A channel with no Setting
// row is in ModeOff.
type Setting struct {
	ChannelID     string `json:"channel_id" db:"channelid"`
	BotID         string `json:"bot_id" db:"botid"`
	Mode          Mode   `json:"mode" db:"mode"`
	UpdatedBy     string `json:"updated_by" db:"updatedby"`
	UpdateAt      int64  `json:"update_at" db:"updateat"`
	Instructions  string `json:"instructions,omitempty" db:"instructions"`
	AnalysisModel string `json:"analysis_model,omitempty" db:"analysismodel"`
}

// Store provides access to the Agents_ChannelAutoReply table. It persists
// settings exactly as given; validation and timestamps are the Service's job.
type Store struct {
	db *mmapi.DBClient
}

// NewStore creates a new channel auto-reply store.
func NewStore(db *mmapi.DBClient) *Store {
	return &Store{db: db}
}

// Get returns the setting for a channel, or (nil, nil) when the channel has no
// row (mode off).
func (s *Store) Get(channelID string) (*Setting, error) {
	var settings []Setting
	if err := s.db.DoQuery(&settings, s.db.Builder().
		Select("ChannelID", "BotID", "Mode", "UpdatedBy", "UpdateAt", "Instructions", "AnalysisModel").
		From("Agents_ChannelAutoReply").
		Where(sq.Eq{"ChannelID": channelID}),
	); err != nil {
		return nil, fmt.Errorf("failed to get channel auto-reply setting: %w", err)
	}

	if len(settings) == 0 {
		return nil, nil
	}

	return &settings[0], nil
}

// Set upserts the setting for setting.ChannelID. The ChannelID primary key
// guarantees at most one auto-replying agent per channel.
func (s *Store) Set(setting Setting) error {
	_, err := s.db.ExecBuilder(s.db.Builder().
		Insert("Agents_ChannelAutoReply").
		Columns("ChannelID", "BotID", "Mode", "UpdatedBy", "UpdateAt", "Instructions", "AnalysisModel").
		Values(setting.ChannelID, setting.BotID, string(setting.Mode), setting.UpdatedBy, setting.UpdateAt, setting.Instructions, setting.AnalysisModel).
		Suffix("ON CONFLICT (ChannelID) DO UPDATE SET BotID = EXCLUDED.BotID, Mode = EXCLUDED.Mode, UpdatedBy = EXCLUDED.UpdatedBy, UpdateAt = EXCLUDED.UpdateAt, Instructions = EXCLUDED.Instructions, AnalysisModel = EXCLUDED.AnalysisModel"))
	if err != nil {
		return fmt.Errorf("failed to set channel auto-reply setting: %w", err)
	}

	return nil
}

// Delete removes the setting for a channel. Deleting a channel with no row is
// a no-op, not an error.
func (s *Store) Delete(channelID string) error {
	_, err := s.db.ExecBuilder(s.db.Builder().
		Delete("Agents_ChannelAutoReply").
		Where(sq.Eq{"ChannelID": channelID}))
	if err != nil {
		return fmt.Errorf("failed to delete channel auto-reply setting: %w", err)
	}

	return nil
}

// ListAll returns every stored setting. Used to warm the in-memory cache at
// plugin activation; the table holds one row per enabled channel, so a full
// scan is cheap.
func (s *Store) ListAll() ([]Setting, error) {
	var settings []Setting
	if err := s.db.DoQuery(&settings, s.db.Builder().
		Select("ChannelID", "BotID", "Mode", "UpdatedBy", "UpdateAt", "Instructions", "AnalysisModel").
		From("Agents_ChannelAutoReply"),
	); err != nil {
		return nil, fmt.Errorf("failed to list channel auto-reply settings: %w", err)
	}

	return settings, nil
}
