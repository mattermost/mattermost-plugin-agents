// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package toolrunner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// roundCapturingLLM records the round index each ChatCompletion call carries.
type roundCapturingLLM struct {
	*testLLM
	rounds []int
}

func (m *roundCapturingLLM) ChatCompletion(ctx context.Context, req llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	round, ok := telemetry.LLMRoundFromContext(ctx)
	if !ok {
		round = -1
	}
	m.rounds = append(m.rounds, round)
	return m.testLLM.ChatCompletion(ctx, req, opts...)
}

func TestToolRunnerTracing(t *testing.T) {
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})

	toolCall := func(id string) []llm.TextStreamEvent {
		return []llm.TextStreamEvent{
			{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{ID: id, Name: "lookup", Arguments: json.RawMessage(`{}`)}}},
			{Type: llm.EventTypeEnd},
		}
	}
	lm := &roundCapturingLLM{testLLM: &testLLM{responses: []testResponse{
		{events: toolCall("tc1")},
		{events: toolCall("tc2")},
		{events: []llm.TextStreamEvent{{Type: llm.EventTypeText, Value: "done"}, {Type: llm.EventTypeEnd}}},
	}}}

	const result = "a tool result of known size"
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{
		Name: "lookup",
		Resolver: func(ctx context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			// Work inside the resolver must nest under the tool's span.
			_, child := telemetry.Tracer().Start(ctx, "mcp call tool")
			child.End()
			return result, nil
		},
	}})

	run, err := New(lm).Run(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
		Context: &llm.Context{Tools: store},
	}, alwaysExecute, nil)
	require.NoError(t, err)
	_, err = run.Stream.ReadAll()
	require.NoError(t, err)

	require.Equal(t, []int{0, 1, 2}, lm.rounds)

	var resolveSpans []tracetest.SpanStub
	children := map[string]string{}
	for _, s := range exporter.GetSpans() {
		switch s.Name {
		case "resolve tool":
			resolveSpans = append(resolveSpans, s)
		case "mcp call tool":
			children[s.Parent.SpanID().String()] = s.Name
		}
	}
	require.Len(t, resolveSpans, 2, "one resolve tool span per call, not nested duplicates")
	for i, s := range resolveSpans {
		attrs := map[string]any{}
		for _, a := range s.Attributes {
			attrs[string(a.Key)] = a.Value.AsInterface()
		}
		require.Equal(t, []string{"tc1", "tc2"}[i], attrs[string(telemetry.ToolID)])
		require.Equal(t, "lookup", attrs[string(telemetry.ToolName)])
		require.Equal(t, "success", attrs[string(telemetry.ToolStatus)])
		require.Equal(t, telemetry.ToolExecutionAuto, attrs[string(telemetry.ToolExecution)])
		require.Equal(t, int64(len(result)), attrs[string(telemetry.ToolResultBytes)])
		require.Contains(t, children, s.SpanContext.SpanID().String())
	}
}

// A stopped reply stops reading the stream; canceling ctx must end the loop
// rather than leave it blocked on a send nobody will receive.
func TestToolRunnerStopsWhenCanceledWhileUnread(t *testing.T) {
	events := make([]llm.TextStreamEvent, 0, 11)
	for range 10 {
		events = append(events, llm.TextStreamEvent{Type: llm.EventTypeText, Value: "chunk"})
	}
	events = append(events, llm.TextStreamEvent{Type: llm.EventTypeEnd})
	lm := &testLLM{responses: []testResponse{{events: events}}}

	ctx, cancel := context.WithCancel(context.Background())
	run, err := New(lm).Run(ctx, llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
		Context: &llm.Context{Tools: llm.NewToolStore()},
	}, alwaysExecute, nil)
	require.NoError(t, err)

	<-run.Stream.Stream // the reply reads one chunk, then is stopped
	cancel()
	// With nobody reading, the loop can only proceed by noticing the cancel.
	time.Sleep(50 * time.Millisecond)

	select {
	case _, open := <-run.Stream.Stream:
		require.False(t, open, "loop kept forwarding after cancel instead of stopping")
	case <-time.After(time.Second):
		require.FailNow(t, "tool loop still blocked after cancel")
	}
}

// A reply stopped while a tool runs cancels the tool; its "context canceled"
// error must not be persisted as a tool failure the next turn would read.
func TestToolRunnerDropsRoundCanceledDuringTool(t *testing.T) {
	lm := &testLLM{responses: []testResponse{{events: []llm.TextStreamEvent{
		{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{ID: "tc1", Name: "slow", Arguments: json.RawMessage(`{}`)}}},
		{Type: llm.EventTypeEnd},
	}}}}

	ctx, cancel := context.WithCancel(context.Background())
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{
		Name: "slow",
		Resolver: func(toolCtx context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			cancel() // the user stops the reply while the tool runs
			<-toolCtx.Done()
			return "", toolCtx.Err()
		},
	}})

	var persisted []ToolTurn
	run, err := New(lm).Run(ctx, llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
		Context: &llm.Context{Tools: store},
	}, alwaysExecute, func(turns []ToolTurn) { persisted = append(persisted, turns...) })
	require.NoError(t, err)

	_, _ = run.Stream.ReadAll() // read until the loop exits
	require.Empty(t, persisted, "a canceled round must not be persisted")
}
