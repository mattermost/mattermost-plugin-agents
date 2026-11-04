// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStreamToPostRecordsOutcome(t *testing.T) {
	const channelID = "channel-id"
	const answer = "secret answer text"

	tests := []struct {
		name        string
		events      []llm.TextStreamEvent
		cancel      bool
		wantOutcome string
		wantLength  bool // the final message is non-empty
	}{
		{
			name:        "completed",
			events:      []llm.TextStreamEvent{{Type: llm.EventTypeText, Value: answer}, {Type: llm.EventTypeEnd}},
			wantOutcome: telemetry.StreamOutcomeCompleted,
			wantLength:  true,
		},
		{
			name:        "empty",
			events:      []llm.TextStreamEvent{{Type: llm.EventTypeEnd}},
			wantOutcome: telemetry.StreamOutcomeEmpty,
			wantLength:  true, // the fallback message
		},
		{
			name:        "errored",
			events:      []llm.TextStreamEvent{{Type: llm.EventTypeError, Value: errors.New("provider down")}},
			wantOutcome: telemetry.StreamOutcomeErrored,
			wantLength:  true,
		},
		{
			name:        "canceled",
			cancel:      true,
			wantOutcome: telemetry.StreamOutcomeCancelled,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			previous := otel.GetTracerProvider()
			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			otel.SetTracerProvider(tp)
			t.Cleanup(func() {
				_ = tp.Shutdown(context.Background())
				otel.SetTracerProvider(previous)
			})

			client := &fakeStreamingClient{channels: map[string]*model.Channel{channelID: {Id: channelID, Type: model.ChannelTypeOpen}}}
			service := NewMMPostStreamService(client, i18n.Init())

			stream := make(chan llm.TextStreamEvent, len(tc.events))
			for _, e := range tc.events {
				stream <- e
			}
			if !tc.cancel {
				close(stream)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancel {
				cancel()
			} else {
				defer cancel()
			}

			post := &model.Post{Id: "post-id", ChannelId: channelID}
			service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			attrs := map[string]any{}
			for _, a := range spans[0].Attributes {
				attrs[string(a.Key)] = a.Value.AsInterface()
				require.NotContains(t, a.Value.String(), answer, "message content must not reach the span")
			}
			require.Equal(t, tc.wantOutcome, attrs[string(telemetry.StreamOutcome)])
			length, ok := attrs[string(telemetry.StreamMessageLength)].(int64)
			require.True(t, ok)
			require.Equal(t, int64(len(post.Message)), length)
			require.Equal(t, tc.wantLength, length > 0)
		})
	}
}
