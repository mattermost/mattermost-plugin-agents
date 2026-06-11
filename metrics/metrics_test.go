// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

var _ Metrics = (*NoopMetrics)(nil)

func TestObserveMCPDynamicToolEventIncrementsCounter(t *testing.T) {
	metricsService := NewMetrics(InstanceInfo{})

	metricsService.ObserveMCPDynamicToolEvent("", "search", "success")

	err := testutil.GatherAndCompare(metricsService.GetRegistry(), strings.NewReader(`
# HELP agents_mcp_dynamic_tool_events_total The total number of MCP dynamic tool loading events.
# TYPE agents_mcp_dynamic_tool_events_total counter
agents_mcp_dynamic_tool_events_total{bot_name="unknown",event="search",result="success"} 1
`), "agents_mcp_dynamic_tool_events_total")
	require.NoError(t, err)
}
