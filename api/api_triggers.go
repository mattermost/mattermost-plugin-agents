// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/scope"
)

// prepareSubscriptions stamps an ID on each inbound subscription that lacks
// one and defaults Event to message_posted so early UIs can omit the field.
// Used by buildAgentConfigForCreate — no prior state to merge against.
func prepareSubscriptions(subs []llm.AgentSubscription) []llm.AgentSubscription {
	if len(subs) == 0 {
		return nil
	}
	out := make([]llm.AgentSubscription, len(subs))
	for i := range subs {
		out[i] = subs[i]
		if out[i].ID == "" {
			out[i].ID = model.NewId()
		}
		if out[i].Event == "" {
			out[i].Event = llm.SubscriptionEventMessagePosted
		}
	}
	return out
}

// prepareSchedules stamps an ID on each inbound schedule and initializes
// NextFireAt for any enabled schedule so the scheduler starts firing at the
// next epoch-aligned bucket.
func prepareSchedules(scheds []llm.AgentSchedule) []llm.AgentSchedule {
	if len(scheds) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]llm.AgentSchedule, len(scheds))
	for i := range scheds {
		out[i] = scheds[i]
		if out[i].ID == "" {
			out[i].ID = model.NewId()
		}
		if out[i].Enabled && out[i].NextFireAt == 0 {
			out[i].NextFireAt = scope.NextFireBucket(out[i].IntervalSeconds, now)
		}
	}
	return out
}

// mergeSubscriptions reconciles incoming subscription updates against the
// existing list by ID: matched rows preserve their server-managed metadata
// (LastFireAt, LastError, LastErrorAt) to avoid blowing away observability
// on every save; unmatched rows are treated as new and get fresh IDs.
// Subscriptions present in prior but absent in next are dropped (client is
// the source of truth for membership, so a delete is implicit).
func mergeSubscriptions(prior, next []llm.AgentSubscription) []llm.AgentSubscription {
	if len(next) == 0 {
		return nil
	}
	byID := make(map[string]llm.AgentSubscription, len(prior))
	for _, s := range prior {
		byID[s.ID] = s
	}
	out := make([]llm.AgentSubscription, len(next))
	for i := range next {
		candidate := next[i]
		if candidate.ID == "" {
			candidate.ID = model.NewId()
		}
		if existing, ok := byID[candidate.ID]; ok {
			candidate.LastFireAt = existing.LastFireAt
			candidate.LastError = existing.LastError
			candidate.LastErrorAt = existing.LastErrorAt
		}
		if candidate.Event == "" {
			candidate.Event = llm.SubscriptionEventMessagePosted
		}
		out[i] = candidate
	}
	return out
}

// mergeSchedules mirrors mergeSubscriptions. NextFireAt is preserved when
// the interval is unchanged (so editing prompt text doesn't reset the
// schedule), and recomputed when the interval changes or the schedule is
// newly enabled.
func mergeSchedules(prior, next []llm.AgentSchedule) []llm.AgentSchedule {
	if len(next) == 0 {
		return nil
	}
	byID := make(map[string]llm.AgentSchedule, len(prior))
	for _, s := range prior {
		byID[s.ID] = s
	}
	now := time.Now()
	out := make([]llm.AgentSchedule, len(next))
	for i := range next {
		candidate := next[i]
		if candidate.ID == "" {
			candidate.ID = model.NewId()
		}
		existing, matched := byID[candidate.ID]
		if matched {
			candidate.LastFireAt = existing.LastFireAt
			candidate.LastError = existing.LastError
			candidate.LastErrorAt = existing.LastErrorAt
			if existing.IntervalSeconds == candidate.IntervalSeconds {
				candidate.NextFireAt = existing.NextFireAt
			}
		}
		if candidate.Enabled && candidate.NextFireAt == 0 {
			candidate.NextFireAt = scope.NextFireBucket(candidate.IntervalSeconds, now)
		}
		if !candidate.Enabled {
			// A disabled schedule shouldn't retain a pending fire; zero it
			// so re-enable picks a fresh bucket.
			candidate.NextFireAt = 0
		}
		out[i] = candidate
	}
	return out
}
