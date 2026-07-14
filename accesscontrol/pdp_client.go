// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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
func (c *PluginAPIClient) EvaluateAccessRequest(ctx context.Context, userID, resourceType, resourceID, action string) (Outcome, error) {
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

	outcome := outcomeFromModel(decision.Outcome)
	span.SetAttributes(telemetry.ABACOutcome.String(string(outcome)))
	return outcome, nil
}

// outcomeFromModel maps the server outcome onto the plugin-local Outcome.
// Unknown values from a future server map to unavailable — the fail-closed
// row of the decision tables.
func outcomeFromModel(o model.AccessDecisionOutcome) Outcome {
	switch o {
	case model.AccessDecisionOutcomeAllow:
		return OutcomeAllow
	case model.AccessDecisionOutcomeDeny:
		return OutcomeDeny
	case model.AccessDecisionOutcomeNoPolicy:
		return OutcomeNoPolicy
	case model.AccessDecisionOutcomeUnavailable:
		return OutcomeUnavailable
	default:
		return OutcomeUnavailable
	}
}
