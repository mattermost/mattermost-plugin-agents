// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package channelcontext stores and serves the context channel members attach
// for agents working in a channel: free-form channel instructions and posts
// pinned to agent context. Agent-context pins are tracked in the plugin's own
// table, separate from Mattermost's pinned messages, so pinning a post for
// agents never changes the channel's pinned messages and vice versa.
package channelcontext

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// MaxInstructionsRunes bounds channel instructions; they are sent with
	// every agent request in the channel.
	MaxInstructionsRunes = 8000
	// MaxPinnedPosts bounds how many posts a channel can pin to agent context.
	MaxPinnedPosts = 10
	// MaxPinnedPostPromptRunes bounds how much of each pinned post is sent to
	// the LLM.
	MaxPinnedPostPromptRunes = 4000
)

var (
	// ErrValidation marks a write rejected for invalid input. API handlers map
	// it to HTTP 400.
	ErrValidation = errors.New("invalid channel context request")
	// ErrPinLimitReached marks a pin rejected because the channel already has
	// MaxPinnedPosts pins.
	ErrPinLimitReached = errors.New("channel agent context pin limit reached")
)

// PinnedPost is a pin together with the post's current content.
type PinnedPost struct {
	Pin
	Post *model.Post
}

// Service validates and persists channel instructions and agent-context pins,
// and assembles them into prompt context. Reads go to the database: they only
// happen when an agent request is built or the settings UI loads, not on the
// per-post hot path.
type Service struct {
	store    *Store
	mmClient mmapi.Client
}

// NewService creates the channel context service.
func NewService(store *Store, mmClient mmapi.Client) *Service {
	return &Service{store: store, mmClient: mmClient}
}

// GetInstructions returns the channel's instructions, or "" when it has none.
func (s *Service) GetInstructions(channelID string) (string, error) {
	instructions, err := s.store.GetInstructions(channelID)
	if err != nil || instructions == nil {
		return "", err
	}
	return instructions.Instructions, nil
}

// SetInstructions validates and stores the channel's instructions and returns
// the stored text. Surrounding whitespace is trimmed; empty instructions
// remove the channel's row. Validation failures wrap ErrValidation.
func (s *Service) SetInstructions(channel *model.Channel, instructions, updatedBy string) (string, error) {
	if err := validateChannel(channel); err != nil {
		return "", err
	}

	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return "", s.store.DeleteInstructions(channel.Id)
	}
	if utf8.RuneCountInString(instructions) > MaxInstructionsRunes {
		return "", fmt.Errorf("instructions exceed the maximum length of %d characters: %w", MaxInstructionsRunes, ErrValidation)
	}

	if err := s.store.SetInstructions(Instructions{
		ChannelID:    channel.Id,
		Instructions: instructions,
		UpdatedBy:    updatedBy,
		UpdateAt:     model.GetMillis(),
	}); err != nil {
		return "", err
	}

	return instructions, nil
}

// ListPinnedPosts returns the channel's pins with their posts, oldest pin
// first. Pins whose post was deleted or no longer belongs to the channel are
// removed; pins whose post cannot be loaded for another reason are skipped.
func (s *Service) ListPinnedPosts(channelID string) ([]PinnedPost, error) {
	pins, err := s.store.ListPins(channelID)
	if err != nil {
		return nil, err
	}

	pinned := make([]PinnedPost, 0, len(pins))
	for _, pin := range pins {
		post, postErr := s.mmClient.GetPost(pin.PostID)
		if postErr != nil && !isNotFound(postErr) {
			s.mmClient.LogWarn("Failed to load channel context pinned post", "channel_id", channelID, "post_id", pin.PostID, "error", postErr.Error())
			continue
		}
		if postErr != nil || post.DeleteAt != 0 || post.ChannelId != channelID {
			if removeErr := s.store.RemovePin(channelID, pin.PostID); removeErr != nil {
				s.mmClient.LogWarn("Failed to remove stale channel context pin", "channel_id", channelID, "post_id", pin.PostID, "error", removeErr.Error())
			}
			continue
		}
		pinned = append(pinned, PinnedPost{Pin: pin, Post: post})
	}

	return pinned, nil
}

// PinPost pins a post to the channel's agent context. Pinning an
// already-pinned post is a no-op success. The post must be a live, non-system
// post in the channel; validation failures wrap ErrValidation, and a full
// channel returns ErrPinLimitReached.
func (s *Service) PinPost(channel *model.Channel, postID, pinnedBy string) error {
	if err := validateChannel(channel); err != nil {
		return err
	}
	if !model.IsValidId(postID) {
		return fmt.Errorf("invalid post ID: %w", ErrValidation)
	}

	post, err := s.mmClient.GetPost(postID)
	if isNotFound(err) {
		return fmt.Errorf("post %s not found: %w", postID, ErrValidation)
	}
	if err != nil {
		return fmt.Errorf("failed to look up post %s: %w", postID, err)
	}
	if post.ChannelId != channel.Id || post.DeleteAt != 0 {
		return fmt.Errorf("post %s is not in channel %s: %w", postID, channel.Id, ErrValidation)
	}
	if post.IsSystemMessage() {
		return fmt.Errorf("system messages cannot be pinned to agent context: %w", ErrValidation)
	}

	added, err := s.store.AddPin(Pin{
		ChannelID: channel.Id,
		PostID:    postID,
		PinnedBy:  pinnedBy,
		PinnedAt:  model.GetMillis(),
	}, MaxPinnedPosts)
	if err != nil || added {
		return err
	}

	alreadyPinned, err := s.store.HasPin(channel.Id, postID)
	if err != nil {
		return err
	}
	if !alreadyPinned {
		return ErrPinLimitReached
	}
	return nil
}

// UnpinPost removes a post from the channel's agent context. Unpinning a post
// that is not pinned is a no-op success, so stale pins are always removable.
func (s *Service) UnpinPost(channelID, postID string) error {
	return s.store.RemovePin(channelID, postID)
}

// PromptContext returns the channel's instructions and pinned posts formatted
// for the system prompt, or nil when the channel has neither.
func (s *Service) PromptContext(channelID string) (*llm.ChannelContext, error) {
	instructions, err := s.GetInstructions(channelID)
	if err != nil {
		return nil, err
	}
	pinned, err := s.ListPinnedPosts(channelID)
	if err != nil {
		return nil, err
	}
	if instructions == "" && len(pinned) == 0 {
		return nil, nil
	}

	posts := make([]*model.Post, 0, len(pinned))
	usernames := make(map[string]string)
	for _, p := range pinned {
		posts = append(posts, p.Post)
		if _, seen := usernames[p.Post.UserId]; seen {
			continue
		}
		usernames[p.Post.UserId] = ""
		if user, userErr := s.mmClient.GetUser(p.Post.UserId); userErr == nil {
			usernames[p.Post.UserId] = user.Username
		}
	}

	return &llm.ChannelContext{
		Instructions: instructions,
		PinnedPosts:  format.ContextPosts(posts, usernames, MaxPinnedPostPromptRunes),
	}, nil
}

func validateChannel(channel *model.Channel) error {
	if channel == nil || channel.Id == "" {
		return fmt.Errorf("channel is required: %w", ErrValidation)
	}
	if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
		return fmt.Errorf("channel context can only be configured for public and private channels: %w", ErrValidation)
	}
	return nil
}

func isNotFound(err error) bool {
	appErr, ok := errors.AsType[*model.AppError](err)
	return ok && appErr.StatusCode == http.StatusNotFound
}
