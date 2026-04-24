// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scope

import (
	"context"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

// AgentLister lists all active agents. Only the subscription/schedule arrays
// and enough identity fields to dispatch are read.
type AgentLister interface {
	ListAgents() ([]*llm.BotConfig, error)
}

// SubscriptionsService converts inbound MessageHasBeenPosted hook calls into
// scope Dispatcher invocations for any subscribed agent. It maintains an
// in-memory channelID → []agentSubscription index rebuilt via Reload.
type SubscriptionsService struct {
	lister     AgentLister
	dispatcher *Dispatcher
	mmClient   mmapi.Client
	log        Logger

	mu    sync.RWMutex
	index map[string][]agentSub
}

type agentSub struct {
	agentID string
	sub     llm.AgentSubscription
}

// NewSubscriptionsService constructs the service. The caller must call
// Reload before the first message arrives to populate the index; typical
// wiring does this from OnActivate and from refreshBotsAndNotify.
func NewSubscriptionsService(
	lister AgentLister,
	dispatcher *Dispatcher,
	mmClient mmapi.Client,
	log Logger,
) *SubscriptionsService {
	return &SubscriptionsService{
		lister:     lister,
		dispatcher: dispatcher,
		mmClient:   mmClient,
		log:        log,
		index:      make(map[string][]agentSub),
	}
}

// Reload rebuilds the channel→subscriptions index from persisted agent rows.
// Callers should invoke this after any agent create/update/delete.
func (s *SubscriptionsService) Reload() error {
	agents, err := s.lister.ListAgents()
	if err != nil {
		return err
	}
	idx := make(map[string][]agentSub)
	for _, cfg := range agents {
		if cfg == nil || cfg.DeleteAt != 0 {
			continue
		}
		for _, sub := range cfg.Subscriptions {
			if !sub.Enabled {
				continue
			}
			if sub.Event != llm.SubscriptionEventMessagePosted {
				continue
			}
			idx[sub.ScopeChannelID] = append(idx[sub.ScopeChannelID], agentSub{
				agentID: cfg.ID,
				sub:     sub,
			})
		}
	}
	s.mu.Lock()
	s.index = idx
	s.mu.Unlock()
	return nil
}

// OnMessagePosted evaluates a post against the subscriptions index and
// dispatches any matching agent in a goroutine. Automated posts (bots,
// webhooks, plugins, OAuth apps) are filtered out to prevent re-entrancy.
func (s *SubscriptionsService) OnMessagePosted(ctx context.Context, post *model.Post) {
	if post == nil || post.ChannelId == "" {
		return
	}

	s.mu.RLock()
	matches := append([]agentSub(nil), s.index[post.ChannelId]...)
	s.mu.RUnlock()
	if len(matches) == 0 {
		return
	}

	// Fetch the posting user to classify automated posts. If lookup fails
	// we err on the side of not firing — automated posts from unresolvable
	// user IDs would be the worst-case trigger for loops.
	postingUser, err := s.mmClient.GetUser(post.UserId)
	if err != nil {
		s.log.Warn("subscriptions: failed to resolve posting user, skipping",
			"channel", post.ChannelId, "user_id", post.UserId, "error", err.Error())
		return
	}
	if isAutomatedInvoker(post, postingUser) {
		return
	}

	channel, err := s.mmClient.GetChannel(post.ChannelId)
	if err != nil || channel == nil {
		s.log.Warn("subscriptions: failed to get channel", "channel", post.ChannelId, "error", errString(err))
		return
	}

	for i := range matches {
		m := matches[i]
		go s.dispatcher.DispatchSubscription(ctx, m.agentID, m.sub, post, postingUser, channel)
	}
}

// isAutomatedInvoker is a small duplicate of conversations.isAutomatedInvoker
// kept here to keep the scope package free of a conversations import cycle.
// Keep these two in sync.
func isAutomatedInvoker(post *model.Post, postingUser *model.User) bool {
	if postingUser != nil && postingUser.IsBot {
		return true
	}
	if post == nil {
		return false
	}
	for _, prop := range []string{"from_webhook", "from_plugin", "from_bot", "from_oauth_app"} {
		if post.GetProp(prop) != nil {
			return true
		}
	}
	return false
}
