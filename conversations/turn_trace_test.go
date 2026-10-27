// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func installTurnTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithIDGenerator(telemetry.NewTurnIDGenerator()),
	)
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})
	return exporter
}

func spanNamed(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()
	var found []tracetest.SpanStub
	for _, s := range spans {
		if s.Name == name {
			found = append(found, s)
		}
	}
	require.Len(t, found, 1, "expected exactly one %q span", name)
	return found[0]
}

func attrMap(attrs []attribute.KeyValue) map[attribute.Key]any {
	out := make(map[attribute.Key]any, len(attrs))
	for _, a := range attrs {
		out[a.Key] = a.Value.AsInterface()
	}
	return out
}

// A DM turn must be reconstructable from its user post ID alone: the message
// hook names the run's trace, the run carries the turn's identifiers and
// links back to the hook, every LLM call (title generation included) runs in
// the run's trace, and the run lasts until its response finishes streaming.
func TestDMTurnTraceIsCorrelated(t *testing.T) {
	exporter := installTurnTracer(t)
	env := setupDMTestEnv(t, dmMakeTextStream("Hello!"))
	env.streamService.finished = make(chan string, 1)
	env.fakeLLM.titleDone = make(chan struct{}, 1)

	post := &model.Post{
		Id:        model.NewId(),
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "Hello bot",
	}
	env.conversations.MessageHasBeenPosted(nil, post)

	select {
	case <-env.streamService.finished:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "response never finished streaming")
	}
	select {
	case <-env.fakeLLM.titleDone:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "title was never generated")
	}

	require.Eventually(t, func() bool {
		for _, s := range exporter.GetSpans() {
			if s.Name == "agent run" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "agent run span never ended")

	spans := exporter.GetSpans()
	hook := spanNamed(t, spans, "message has been posted")
	run := spanNamed(t, spans, "agent run")
	process := spanNamed(t, spans, "process dm request")

	hookAttrs := attrMap(hook.Attributes)
	runAttrs := attrMap(run.Attributes)
	runTraceID := run.SpanContext.TraceID()

	// From the post ID to the run.
	require.Equal(t, post.Id, hookAttrs[telemetry.PostID])
	require.Equal(t, runTraceID.String(), hookAttrs[telemetry.RunTraceID])

	// The run identifies the turn.
	require.Equal(t, post.Id, runAttrs[telemetry.PostID])
	require.Equal(t, env.channelID, runAttrs[telemetry.ChannelID])
	require.Equal(t, env.userID, runAttrs[telemetry.UserID])
	require.NotEmpty(t, runAttrs[telemetry.ConversationID])
	require.NotEmpty(t, runAttrs[telemetry.UserTurnID])
	require.Equal(t, hookAttrs[telemetry.ConversationID], runAttrs[telemetry.ConversationID])

	// And links back to the hook.
	var linkedToHook bool
	for _, l := range run.Links {
		if l.SpanContext.SpanID() == hook.SpanContext.SpanID() {
			linkedToHook = true
		}
	}
	require.True(t, linkedToHook, "agent run must link to the message hook span")

	// Progress phases are recorded on the run, not the hook.
	require.Empty(t, hook.Events)
	require.Len(t, run.Events, 4)

	// Every LLM call, including title generation, is in the run's trace.
	env.fakeLLM.mu.Lock()
	callTraces := append([]trace.TraceID(nil), env.fakeLLM.callTraces...)
	env.fakeLLM.mu.Unlock()
	require.Len(t, callTraces, 2)
	for _, tid := range callTraces {
		require.Equal(t, runTraceID, tid)
	}

	// Durations cover the streamed work.
	require.Equal(t, runTraceID, process.SpanContext.TraceID())
	// process dm request ends when the stream closes and the run once
	// streaming finishes, so both outlast the LLM call.
	require.False(t, run.EndTime.Before(process.EndTime), "agent run must not end before process dm request")
}

// The bot's own response placeholder triggers the post hook too; it must not
// leave an unlinked trace behind.
func TestBotPostLeavesNoTrace(t *testing.T) {
	exporter := installTurnTracer(t)
	env := setupDMTestEnv(t)

	env.conversations.MessageHasBeenPosted(nil, &model.Post{
		Id:        model.NewId(),
		UserId:    env.botID,
		ChannelId: env.channelID,
	})

	require.Empty(t, exporter.GetSpans())
}
