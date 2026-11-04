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

	var payloads []map[string]any
	client := mocks.NewMockClient(t)
	client.On("PublishWebSocketEvent", "postupdate", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			payloads = append(payloads, args.Get(1).(map[string]any))
			broadcast := args.Get(2).(*model.WebsocketBroadcast)
			require.Equal(t, "channel-id", broadcast.ChannelId)
			require.True(t, broadcast.ReliableClusterSend)
		}).
		Times(4)

	ctx, hookSpan := telemetry.Tracer().Start(context.Background(), "message has been posted")
	reporter := newResponseProgressReporter(ctx, client, &model.Post{Id: "post-id", ChannelId: "channel-id"})
	reporter.Advance(responseProgressCheckingMCP)
	reporter.Advance(responseProgressLoadingConversation)
	reporter.Advance(responseProgressCheckingMCP)
	// The run starts mid-way; earlier phases must move onto it.
	_, runSpan := telemetry.Tracer().Start(context.Background(), "agent run")
	reporter.AttachSpan(runSpan)
	reporter.Advance(responseProgressPreparingRequest)
	reporter.Advance(responseProgressConnectingProvider)
	reporter.Advance(responseProgressPreparingRequest)
	reporter.Flush()
	runSpan.End()
	hookSpan.End()

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
	require.Len(t, spans, 2)
	var run, hook tracetest.SpanStub
	for _, s := range spans {
		if s.Name == "agent run" {
			run = s
		} else {
			hook = s
		}
	}
	require.Empty(t, hook.Events, "progress events belong on the agent run, not the message hook")
	require.Len(t, run.Events, len(expectedPhases))
	for i, expectedPhase := range expectedPhases {
		event := run.Events[i]
		require.Equal(t, "agent.progress", event.Name)
		require.Equal(t, string(expectedPhase), event.Attributes[0].Value.AsString())
		require.Equal(t, int64(i+1), event.Attributes[1].Value.AsInt64())
	}
}

func TestResponseProgressReporterFlushesToCreatingSpanWithoutRun(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	client := mocks.NewMockClient(t)
	client.On("PublishWebSocketEvent", "postupdate", mock.Anything, mock.Anything).Times(2)

	ctx, hookSpan := telemetry.Tracer().Start(context.Background(), "message has been posted")
	reporter := newResponseProgressReporter(ctx, client, &model.Post{Id: "post-id", ChannelId: "channel-id"})
	reporter.Advance(responseProgressCheckingMCP)
	reporter.Advance(responseProgressLoadingConversation)
	// The turn failed before an agent run started.
	reporter.Flush()
	hookSpan.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 2)
}
