// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// errRPCTransportFailure surfaces the generated plugin RPC client's silent
// failure mode: on a transport error it logs, swallows the error, and returns
// (nil, nil). The checkers' error path denies, which is the correct
// fail-closed handling for a decision that never happened.
var errRPCTransportFailure = errors.New("plugin RPC transport failure: EvaluateAccessControl returned no decision")

// PluginAPIClient implements DecisionClient over plugin.API.EvaluateAccessControl.
type PluginAPIClient struct {
	papi plugin.API
}

// NewPluginAPIClient builds a DecisionClient backed by the raw plugin API.
func NewPluginAPIClient(papi plugin.API) *PluginAPIClient {
	return &PluginAPIClient{papi: papi}
}

// EvaluateAccessRequest proxies one PDP decision call. The ctx is used only
// for the span: the plugin RPC hop is synchronous and carries no context.
// The server outcome is returned verbatim: unknown values from a future
// server fall into the checkers' default switch row, which denies —
// preserving fail-closed normalization without a translation layer.
func (c *PluginAPIClient) EvaluateAccessRequest(ctx context.Context, userID, resourceType, resourceID, action string) (model.AccessDecisionOutcome, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac evaluate", trace.WithAttributes(
		telemetry.UserID.String(userID),
		telemetry.ABACResourceType.String(resourceType),
		telemetry.ABACResourceID.String(resourceID),
	))
	defer span.End()

	decision, appErr := c.papi.EvaluateAccessControl(userID, resourceType, resourceID, action)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "access control evaluation failed")
		return "", appErr
	}
	if decision == nil {
		// The generated RPC client returns (nil, nil) on transport failure.
		span.RecordError(errRPCTransportFailure)
		span.SetStatus(codes.Error, "access control evaluation returned no decision")
		return "", errRPCTransportFailure
	}

	span.SetAttributes(telemetry.ABACOutcome.String(string(decision.Outcome)))
	return decision.Outcome, nil
}
