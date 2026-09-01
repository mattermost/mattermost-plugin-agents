// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestResponseProgressReporterAdvancesMonotonically(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	var payloads []map[string]interface{}
	client := mocks.NewMockClient(t)
	client.On("PublishWebSocketEvent", "postupdate", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			payloads = append(payloads, args.Get(1).(map[string]interface{}))
			broadcast := args.Get(2).(*model.WebsocketBroadcast)
			require.Equal(t, "channel-id", broadcast.ChannelId)
			require.True(t, broadcast.ReliableClusterSend)
		}).
		Times(4)

	ctx, span := telemetry.Tracer().Start(context.Background(), "message has been posted")
	reporter := newResponseProgressReporter(ctx, client, &model.Post{Id: "post-id", ChannelId: "channel-id"})
	reporter.Advance(responseProgressCheckingMCP)
	reporter.Advance(responseProgressLoadingConversation)
	reporter.Advance(responseProgressCheckingMCP)
	reporter.Advance(responseProgressPreparingRequest)
	reporter.Advance(responseProgressConnectingProvider)
	reporter.Advance(responseProgressPreparingRequest)
	span.End()

	expectedPhases := []responseProgressPhase{
		responseProgressCheckingMCP,
		responseProgressLoadingConversation,
		responseProgressPreparingRequest,
		responseProgressConnectingProvider,
	}
	require.Len(t, payloads, len(expectedPhases))
	for i, expectedPhase := range expectedPhases {
		require.Equal(t, string(expectedPhase), payloads[i]["progress_phase"])
		require.Equal(t, i+1, payloads[i]["progress_seq"])
	}

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, len(expectedPhases))
	for i, expectedPhase := range expectedPhases {
		event := spans[0].Events[i]
		require.Equal(t, "agent.progress", event.Name)
		require.Equal(t, string(expectedPhase), event.Attributes[0].Value.AsString())
		require.Equal(t, int64(i+1), event.Attributes[1].Value.AsInt64())
	}
}
