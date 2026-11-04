// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestMetaToolSpansNameTheTools(t *testing.T) {
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})

	const secretQuery = "sentinel-query-text"
	registryTool := testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com")
	metaTools := NewMetaTools(NewToolRegistry([]llm.Tool{registryTool}))
	llmContext := &llm.Context{Tools: llm.NewToolStore()}
	llmContext.Tools.AddTools(metaTools)
	llmContext.Tools.SetUnloadedMCPTools([]llm.Tool{registryTool})

	_, err := llmContext.Tools.ResolveTool(context.Background(), SearchToolsName, metaToolArgs(`{"query":"jira issue `+secretQuery+`"}`), llmContext)
	require.NoError(t, err)
	_, err = llmContext.Tools.ResolveTool(context.Background(), LoadToolName, metaToolArgs(`{"name":"jira__get_issue"}`), llmContext)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	search, load := map[string]any{}, map[string]any{}
	for _, a := range spans[0].Attributes {
		search[string(a.Key)] = a.Value.AsInterface()
		require.NotContains(t, a.Value.String(), secretQuery, "the search query must not reach the span")
	}
	for _, a := range spans[1].Attributes {
		load[string(a.Key)] = a.Value.AsInterface()
	}
	require.Equal(t, []string{"jira__get_issue"}, search[string(telemetry.ToolReturnedNames)])
	require.Equal(t, "jira__get_issue", load[string(telemetry.ToolLoadedName)])
}
