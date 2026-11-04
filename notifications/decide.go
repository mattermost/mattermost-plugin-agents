// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package notifications decides who is notified about an agent response and
// delivers those notifications once the response has its final content.
package notifications

// Outcome is how a run on an agent post ended.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeEmpty     Outcome = "empty"
	OutcomeErrored   Outcome = "errored"
	// OutcomeCancelled is a cancellation the requester did not ask for, such
	// as a plugin shutdown or a superseded run.
	OutcomeCancelled Outcome = "canceled"
	// OutcomeStoppedByUser is a cancellation requested by the requester.
	OutcomeStoppedByUser Outcome = "stopped_by_user"
)

// StreamEnd describes the end of one run on an agent post: a stream ending,
// a pause for a decision by the requester, or a failure before streaming.
type StreamEnd struct {
	PostID      string
	ChannelID   string
	RequesterID string
	Outcome     Outcome
	// PausedForRequester is set when the run stopped to wait for the
	// requester: a tool approval, an interaction tool or a share decision.
	PausedForRequester bool
	// UpdateFailed is set when the post's final content could not be saved.
	UpdateFailed bool
	// PostDeferred reports whether the post still has its notifications
	// deferred, i.e. no run on it has delivered to the channel yet.
	PostDeferred bool
}

// Delivery is who is notified at the end of a run.
type Delivery int

const (
	DeliveryNone Delivery = iota
	// DeliveryEveryone releases the deferred post to the whole channel.
	DeliveryEveryone
	// DeliveryRequesterOnly notifies only the user who asked for the response.
	DeliveryRequesterOnly
)

func (d Delivery) String() string {
	switch d {
	case DeliveryEveryone:
		return "everyone"
	case DeliveryRequesterOnly:
		return "requester_only"
	default:
		return "none"
	}
}

// Decide returns who to notify at the end of a run. The channel is notified
// only the first time a post ends with a final answer; every other ending
// that needs attention notifies only the requester, and a stop requested by
// the requester notifies nobody.
func Decide(end StreamEnd) Delivery {
	if end.Outcome == OutcomeStoppedByUser {
		return DeliveryNone
	}
	if end.UpdateFailed || end.Outcome != OutcomeCompleted || end.PausedForRequester {
		return DeliveryRequesterOnly
	}
	if end.PostDeferred {
		return DeliveryEveryone
	}
	return DeliveryRequesterOnly
}
