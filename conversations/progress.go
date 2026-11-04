// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/trace"
)

type responseProgressPhase string

const (
	responseProgressCheckingMCP         responseProgressPhase = "checking_mcp"
	responseProgressLoadingConversation responseProgressPhase = "loading_conversation"
	responseProgressPreparingRequest    responseProgressPhase = "preparing_request"
	responseProgressConnectingProvider  responseProgressPhase = "connecting_provider"
)

var responseProgressSequence = map[responseProgressPhase]int{
	responseProgressCheckingMCP:         1,
	responseProgressLoadingConversation: 2,
	responseProgressPreparingRequest:    3,
	responseProgressConnectingProvider:  4,
}

// responseProgressReporter publishes progress phases to the webapp and records
// them as span events on the agent run. The early phases happen before the
// run's span exists (the run's trace is derived from the user turn, which is
// only known once the conversation is loaded), so events are buffered with
// their original timestamps until AttachSpan; Flush writes any still-buffered
// events to the span the reporter was created under when no run started.
type responseProgressReporter struct {
	client   mmapi.Client
	post     *model.Post
	fallback trace.Span
	span     trace.Span
	pending  []pendingProgressEvent
	sequence int
}

type pendingProgressEvent struct {
	at    time.Time
	attrs []trace.EventOption
}

func newResponseProgressReporter(ctx context.Context, client mmapi.Client, post *model.Post) *responseProgressReporter {
	return &responseProgressReporter{
		client:   client,
		post:     post,
		fallback: telemetry.SpanFromContext(ctx),
	}
}

// AttachSpan directs this and all earlier progress events to span.
func (r *responseProgressReporter) AttachSpan(span trace.Span) {
	r.span = span
	r.flushTo(span)
}

// Flush writes buffered events to the creating span if no run span was
// attached, so failures before the run started keep their progress record.
func (r *responseProgressReporter) Flush() {
	if r.span == nil {
		r.flushTo(r.fallback)
	}
}

func (r *responseProgressReporter) flushTo(span trace.Span) {
	for _, e := range r.pending {
		span.AddEvent("agent.progress", append(e.attrs, trace.WithTimestamp(e.at))...)
	}
	r.pending = nil
}

func (r *responseProgressReporter) Advance(phase responseProgressPhase) {
	sequence := responseProgressSequence[phase]
	if sequence <= r.sequence {
		return
	}
	r.sequence = sequence

	attrs := []trace.EventOption{trace.WithAttributes(
		telemetry.AgentProgressPhase.String(string(phase)),
		telemetry.AgentProgressSequence.Int(sequence),
		telemetry.PostID.String(r.post.Id),
	)}
	if r.span != nil {
		r.span.AddEvent("agent.progress", attrs...)
	} else {
		r.pending = append(r.pending, pendingProgressEvent{at: time.Now(), attrs: attrs})
	}
	r.client.PublishWebSocketEvent("postupdate", map[string]any{
		"post_id":        r.post.Id,
		"control":        "progress",
		"progress_phase": string(phase),
		"progress_seq":   sequence,
	}, &model.WebsocketBroadcast{
		ChannelId:           r.post.ChannelId,
		ReliableClusterSend: true,
	})
}
