// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package autoreply stores and serves per-channel agent auto-reply settings.
// Writes are validated and persisted to Postgres; reads on the message trigger
// path are served from an in-memory cache that is invalidated across cluster
// nodes via plugin cluster events.
package autoreply

import (
	"errors"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrValidation marks a Set rejection caused by invalid input (unknown mode,
// missing bot, DM/GM channel, or the bot's channel restrictions). API handlers
// use errors.Is(err, ErrValidation) to map failures to HTTP 400.
var ErrValidation = errors.New("invalid channel auto-reply setting")

// ClusterNotifier broadcasts a per-channel cache invalidation to peer cluster
// nodes. Implemented by the Plugin in server/cluster_events.go. The event does
// not reach the originating node, which updates its own cache directly.
type ClusterNotifier interface {
	PublishChannelAutoReplyInvalidate(channelID string) error
}

// Service validates, persists, and caches per-channel auto-reply settings.
//
// Locking model: cache is a plain map guarded by mu. GetCached takes RLock
// only. Set, Delete, and RefreshChannel hold the write lock across their DB
// operation *and* the cache mutation so the local cache always converges to
// the last DB write; writes are rare (channel-settings changes), so blocking
// hot-path readers for one DB round-trip is acceptable.
type Service struct {
	store    *Store
	bots     *bots.MMBots
	mmClient mmapi.Client
	notifier ClusterNotifier

	mu    sync.RWMutex
	cache map[string]Setting
}

// NewService creates the auto-reply service. Call LoadCache before serving
// reads.
func NewService(store *Store, botsService *bots.MMBots, mmClient mmapi.Client, notifier ClusterNotifier) *Service {
	return &Service{
		store:    store,
		bots:     botsService,
		mmClient: mmClient,
		notifier: notifier,
		cache:    make(map[string]Setting),
	}
}

// LoadCache replaces the in-memory cache with the full table contents. Called
// once from OnActivate after migrations have run.
func (s *Service) LoadCache() error {
	settings, err := s.store.ListAll()
	if err != nil {
		return fmt.Errorf("failed to load channel auto-reply cache: %w", err)
	}

	cache := make(map[string]Setting, len(settings))
	for _, setting := range settings {
		cache[setting.ChannelID] = setting
	}

	s.mu.Lock()
	s.cache = cache
	s.mu.Unlock()

	return nil
}

// GetCached returns the cached setting for a channel. ok is false when the
// channel has auto-reply off (no row). This is the hot-path read — it is
// called from MessageHasBeenPosted for every post server-wide and MUST NOT
// touch the database.
func (s *Service) GetCached(channelID string) (Setting, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	setting, ok := s.cache[channelID]
	return setting, ok
}

// Get returns the persisted setting for a channel, or (nil, nil) when the
// channel has no setting (mode off). Reads the database, not the cache, so
// API reads are read-your-writes across cluster nodes.
func (s *Service) Get(channelID string) (*Setting, error) {
	return s.store.Get(channelID)
}

// Set validates and persists an auto-reply setting for a channel, updates the
// local cache, and broadcasts an invalidation to peer nodes. mode must be
// ModeRootPosts or ModeThreads; turning auto-reply off is Delete, not
// Set(ModeOff). Validation failures wrap ErrValidation. Returns the stored
// setting (with UpdateAt populated).
func (s *Service) Set(channelID, botID string, mode Mode, updatedBy string) (*Setting, error) {
	if channelID == "" {
		return nil, fmt.Errorf("channel ID is required: %w", ErrValidation)
	}
	if botID == "" {
		return nil, fmt.Errorf("bot ID is required: %w", ErrValidation)
	}
	if !mode.IsStorable() {
		return nil, fmt.Errorf("mode %q is not a storable auto-reply mode: %w", mode, ErrValidation)
	}

	bot := s.bots.GetBotByID(botID)
	if bot == nil {
		return nil, fmt.Errorf("bot %s not found: %w", botID, ErrValidation)
	}

	channel, err := s.mmClient.GetChannel(channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to look up channel %s: %w", channelID, err)
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return nil, fmt.Errorf("auto-reply cannot be configured for direct or group channels: %w", ErrValidation)
	}

	if restrictionErr := s.bots.CheckUsageRestrictionsForChannel(bot, channel); restrictionErr != nil {
		return nil, fmt.Errorf("bot is not allowed in this channel (%v): %w", restrictionErr, ErrValidation)
	}

	setting := Setting{
		ChannelID: channelID,
		BotID:     botID,
		Mode:      mode,
		UpdatedBy: updatedBy,
		UpdateAt:  model.GetMillis(),
	}

	s.mu.Lock()
	if storeErr := s.store.Set(setting); storeErr != nil {
		s.mu.Unlock()
		return nil, storeErr
	}
	s.cache[channelID] = setting
	s.mu.Unlock()

	s.notifyPeers(channelID)

	return &setting, nil
}

// Delete removes a channel's auto-reply setting (mode off), updates the local
// cache, and broadcasts an invalidation to peer nodes. Deleting a channel with
// no setting is a no-op success, so stale settings are always removable even
// when the bot or channel no longer passes Set validation.
func (s *Service) Delete(channelID string) error {
	if channelID == "" {
		return fmt.Errorf("channel ID is required: %w", ErrValidation)
	}

	s.mu.Lock()
	if err := s.store.Delete(channelID); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.cache, channelID)
	s.mu.Unlock()

	s.notifyPeers(channelID)

	return nil
}

// RefreshChannel re-reads one channel's row from the database and updates the
// cache to match. Called by the cluster event handler when a peer node changed
// the setting.
func (s *Service) RefreshChannel(channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	setting, err := s.store.Get(channelID)
	if err != nil {
		return err
	}

	if setting == nil {
		delete(s.cache, channelID)
		return nil
	}
	s.cache[channelID] = *setting

	return nil
}

// notifyPeers broadcasts the invalidation and logs on failure. A failed
// broadcast never fails the write: the DB row is durable and peers converge on
// the next invalidation or plugin restart.
func (s *Service) notifyPeers(channelID string) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.PublishChannelAutoReplyInvalidate(channelID); err != nil {
		s.mmClient.LogWarn("Failed to broadcast channel auto-reply invalidation; peer nodes may serve a stale setting", "channel_id", channelID, "error", err.Error())
	}
}
