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
// (nil, nil) transport-failure mode; the checkers' error path denies it.
var errRPCTransportFailure = errors.New("plugin RPC transport failure: EvaluateAccessControl returned no decision")

type PluginAPIClient struct {
	papi plugin.API
}

func NewPluginAPIClient(papi plugin.API) *PluginAPIClient {
	return &PluginAPIClient{papi: papi}
}

// EvaluateAccessRequest proxies one PDP call. ctx is span-only; the plugin RPC
// hop carries no context. A missing decision is an error (checkers deny).
func (c *PluginAPIClient) EvaluateAccessRequest(ctx context.Context, userID, resourceType, resourceID, action string) (*model.AccessDecision, error) {
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
		return nil, appErr
	}
	if decision == nil {
		// The generated RPC client returns (nil, nil) on transport failure.
		span.RecordError(errRPCTransportFailure)
		span.SetStatus(codes.Error, "access control evaluation returned no decision")
		return nil, errRPCTransportFailure
	}

	span.SetAttributes(telemetry.ABACOutcome.String(outcomeAttribute(*decision)))
	return decision, nil
}

// outcomeAttribute keeps allow/deny/no_policy distinct for the ABACOutcome span
// attribute; AuthZEN folds them into one boolean.
func outcomeAttribute(decision model.AccessDecision) string {
	switch {
	case decision.IsNoPolicy():
		return string(model.AccessDecisionReasonNoPolicy)
	case decision.Decision:
		return "allow"
	default:
		return "deny"
	}
}
