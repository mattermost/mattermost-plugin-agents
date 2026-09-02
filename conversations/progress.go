// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"

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

type responseProgressReporter struct {
	client   mmapi.Client
	post     *model.Post
	span     trace.Span
	sequence int
}

func newResponseProgressReporter(ctx context.Context, client mmapi.Client, post *model.Post) *responseProgressReporter {
	return &responseProgressReporter{
		client: client,
		post:   post,
		span:   telemetry.SpanFromContext(ctx),
	}
}

func (r *responseProgressReporter) Advance(phase responseProgressPhase) {
	sequence := responseProgressSequence[phase]
	if sequence <= r.sequence {
		return
	}
	r.sequence = sequence

	r.span.AddEvent("agent.progress",
		trace.WithAttributes(
			telemetry.AgentProgressPhase.String(string(phase)),
			telemetry.AgentProgressSequence.Int(sequence),
			telemetry.PostID.String(r.post.Id),
		),
	)
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
