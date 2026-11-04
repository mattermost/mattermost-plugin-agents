// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func installTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})
	return exporter
}

func spanAttr(s tracetest.SpanStub, key string) (any, bool) {
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			return a.Value.AsInterface(), true
		}
	}
	return nil, false
}

func TestCheckerSpanStatus(t *testing.T) {
	serverID := model.NewId()
	agentID := model.NewId()
	tests := []struct {
		name        string
		client      *stubDecisionClient
		check       func(c *Checker) error
		wantDenied  bool
		wantErrCode codes.Code
	}{
		{
			name:   "mcp deny is not an error",
			client: &stubDecisionClient{decision: denyDecision()},
			check: func(c *Checker) error {
				return c.CanUseMCPServer(WithResourceName(context.Background(), "jira"), testUserID, serverID)
			},
			wantDenied:  true,
			wantErrCode: codes.Unset,
		},
		{
			name:   "agent deny is not an error",
			client: &stubDecisionClient{decision: denyDecision()},
			check: func(c *Checker) error {
				return c.CanUseAgent(context.Background(), testUserID, &llm.BotConfig{ID: agentID}, nil)
			},
			wantDenied:  true,
			wantErrCode: codes.Unset,
		},
		{
			name:   "evaluation failure is an error",
			client: &stubDecisionClient{err: errors.New("rpc down")},
			check: func(c *Checker) error {
				return c.CanUseMCPServer(context.Background(), testUserID, serverID)
			},
			wantDenied:  true,
			wantErrCode: codes.Error,
		},
		{
			name:   "allow",
			client: &stubDecisionClient{decision: allowDecision()},
			check: func(c *Checker) error {
				return c.CanUseMCPServer(context.Background(), testUserID, serverID)
			},
			wantErrCode: codes.Unset,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := installTestTracer(t)
			err := tc.check(New(tc.client, nil, NoMCPServerIDs, nil))
			require.Equal(t, tc.wantDenied, errors.Is(err, ErrAccessDenied))

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			require.Equal(t, tc.wantErrCode, spans[0].Status.Code)
			denied, _ := spanAttr(spans[0], string(telemetry.ABACDenied))
			require.Equal(t, tc.wantDenied, denied == true)
		})
	}
}

func TestCheckerSpanCarriesResourceName(t *testing.T) {
	exporter := installTestTracer(t)
	c := New(&stubDecisionClient{decision: denyDecision()}, nil, NoMCPServerIDs, nil)

	_ = c.CanUseMCPServer(WithResourceName(context.Background(), "jira"), testUserID, model.NewId())

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	name, ok := spanAttr(spans[0], string(telemetry.ABACResourceName))
	require.True(t, ok)
	require.Equal(t, "jira", name)
}

// A legacy allow/block-list denial must mark the span as a denial so it is
// visible (and survives export filtering) like a policy denial.
func TestCanUseAgentLegacyDenialMarksSpan(t *testing.T) {
	exporter := installTestTracer(t)
	c := New(&stubDecisionClient{decision: noPolicyDecision()}, nil, NoMCPServerIDs, nil)
	legacyErr := errors.New("user not allowed")

	err := c.CanUseAgent(context.Background(), testUserID, &llm.BotConfig{ID: model.NewId()}, func() error { return legacyErr })
	require.ErrorIs(t, err, legacyErr)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Unset, spans[0].Status.Code)
	denied, _ := spanAttr(spans[0], string(telemetry.ABACDenied))
	require.Equal(t, true, denied)
	source, _ := spanAttr(spans[0], string(telemetry.ABACDecisionSource))
	require.Equal(t, "legacy", source)
}

func TestIsAvailableSpanRecordsProbeResult(t *testing.T) {
	tests := []struct {
		name          string
		appErr        *model.AppError
		wantAvailable bool
	}{
		{name: "available", wantAvailable: true},
		{
			name:   "unlicensed",
			appErr: model.NewAppError("Init", "app.pap.init.app_error", nil, "enterprise advanced license required", http.StatusNotImplemented),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := installTestTracer(t)
			api := &plugintest.API{}
			var ast *model.VisualExpression
			if tc.appErr == nil {
				ast = &model.VisualExpression{}
			}
			api.On("GetAccessControlVisualAST", mock.Anything, ResourceTypeAgent, availabilityProbeExpression).Return(ast, tc.appErr).Once()

			c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)
			require.Equal(t, tc.wantAvailable, c.IsAvailable(context.Background(), model.NewId()))

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			require.Equal(t, codes.Unset, spans[0].Status.Code, "an unavailable ABAC is an answer, not a failure")
			available, _ := spanAttr(spans[0], string(telemetry.ABACAvailable))
			require.Equal(t, tc.wantAvailable, available)
			status, hasStatus := spanAttr(spans[0], string(telemetry.ABACProbeStatusCode))
			errID, _ := spanAttr(spans[0], string(telemetry.ABACProbeErrorID))
			if tc.appErr == nil {
				require.False(t, hasStatus)
				return
			}
			require.Equal(t, int64(tc.appErr.StatusCode), status)
			require.Equal(t, tc.appErr.Id, errID)
		})
	}
}
