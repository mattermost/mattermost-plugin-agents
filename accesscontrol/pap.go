// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ErrPolicyNotFound is returned by GetPolicy/DeletePolicy when no policy is
// stored under the resource ID (or the stored policy is not plugin-owned).
var ErrPolicyNotFound = errors.New("access policy not found")

// errNoPluginAPI is returned by mutating/query PAP methods on a checker built
// without a plugin API (passthrough test wiring); reads report not-found.
var errNoPluginAPI = errors.New("access control plugin API is not available")

// PAP (policy administration) proxying lives on the Checker — not in the api
// package — so the api package never holds a raw plugin.API handle.

func isNotFoundAppErr(appErr *model.AppError) bool {
	return appErr != nil && appErr.StatusCode == http.StatusNotFound
}

// SavePolicy overwrites the identity fields (ID, Type, Version, Active),
// defaults an empty Name to defaultName, and persists the policy in a single
// plugin-API call — the server owns policy existence, so no plugin-side
// bookkeeping is needed.
func (c *Checker) SavePolicy(ctx context.Context, actingUserID, resourceType, resourceID, defaultName string, policy *model.AccessControlPolicy) (*model.AccessControlPolicy, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac save_policy", trace.WithAttributes(
		telemetry.UserID.String(actingUserID),
		telemetry.ABACResourceType.String(resourceType),
		telemetry.ABACResourceID.String(resourceID),
	))
	defer span.End()

	if c.papi == nil {
		return nil, errNoPluginAPI
	}

	policy.ID = resourceID
	policy.Type = resourceType
	policy.Version = model.AccessControlPolicyVersionV0_5
	policy.Active = true
	if policy.Name == "" {
		policy.Name = defaultName
	}

	saved, appErr := c.papi.SaveAccessControlPolicy(actingUserID, policy)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "policy save failed")
		return nil, appErr
	}
	return saved, nil
}

// GetPolicy returns the stored policy or ErrPolicyNotFound. Ownership/type
// scoping is enforced server-side.
func (c *Checker) GetPolicy(ctx context.Context, resourceID string) (*model.AccessControlPolicy, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac get_policy", trace.WithAttributes(
		telemetry.ABACResourceID.String(resourceID),
	))
	defer span.End()

	if c.papi == nil {
		return nil, ErrPolicyNotFound
	}

	policy, appErr := c.papi.GetAccessControlPolicy(resourceID)
	if appErr != nil {
		if isNotFoundAppErr(appErr) {
			return nil, ErrPolicyNotFound
		}
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "policy get failed")
		return nil, appErr
	}
	return policy, nil
}

// DeletePolicy deletes the stored policy. Returns ErrPolicyNotFound when no
// policy exists.
func (c *Checker) DeletePolicy(ctx context.Context, actingUserID, resourceType, resourceID string) error {
	_, span := telemetry.Tracer().Start(ctx, "abac delete_policy", trace.WithAttributes(
		telemetry.UserID.String(actingUserID),
		telemetry.ABACResourceType.String(resourceType),
		telemetry.ABACResourceID.String(resourceID),
	))
	defer span.End()

	if c.papi == nil {
		return ErrPolicyNotFound
	}

	if appErr := c.papi.DeleteAccessControlPolicy(actingUserID, resourceType, resourceID); appErr != nil {
		if isNotFoundAppErr(appErr) {
			return ErrPolicyNotFound
		}
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "policy delete failed")
		return appErr
	}
	return nil
}

// CheckExpression lints a CEL expression; an empty slice means valid.
func (c *Checker) CheckExpression(ctx context.Context, actingUserID, resourceType, expression string) ([]model.CELExpressionError, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac cel_check", trace.WithAttributes(
		telemetry.UserID.String(actingUserID),
		telemetry.ABACResourceType.String(resourceType),
	))
	defer span.End()

	if c.papi == nil {
		return nil, errNoPluginAPI
	}

	result, appErr := c.papi.CheckAccessControlExpression(actingUserID, resourceType, expression)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "cel check failed")
		return nil, appErr
	}
	return result, nil
}

// TestExpression returns the users matching a CEL expression (test modal).
func (c *Checker) TestExpression(ctx context.Context, actingUserID, resourceType, expression, term, cursorID string, limit int) (*model.AccessControlPolicyTestResponse, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac cel_test", trace.WithAttributes(
		telemetry.UserID.String(actingUserID),
		telemetry.ABACResourceType.String(resourceType),
	))
	defer span.End()

	if c.papi == nil {
		return nil, errNoPluginAPI
	}

	result, appErr := c.papi.QueryUsersForAccessControlExpression(actingUserID, resourceType, expression, term, cursorID, limit)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "cel test failed")
		return nil, appErr
	}
	return result, nil
}

// FieldsAutocomplete returns CPA fields for editor autocomplete.
func (c *Checker) FieldsAutocomplete(ctx context.Context, actingUserID, after string, limit int) ([]*model.PropertyField, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac cel_fields", trace.WithAttributes(
		telemetry.UserID.String(actingUserID),
	))
	defer span.End()

	if c.papi == nil {
		return nil, errNoPluginAPI
	}

	result, appErr := c.papi.GetAccessControlFieldsAutocomplete(actingUserID, after, limit)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "cel fields autocomplete failed")
		return nil, appErr
	}
	return result, nil
}

// VisualAST converts a CEL expression to the visual (table) AST.
func (c *Checker) VisualAST(ctx context.Context, actingUserID, resourceType, expression string) (*model.VisualExpression, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac cel_visual_ast", trace.WithAttributes(
		telemetry.UserID.String(actingUserID),
		telemetry.ABACResourceType.String(resourceType),
	))
	defer span.End()

	if c.papi == nil {
		return nil, errNoPluginAPI
	}

	result, appErr := c.papi.GetAccessControlVisualAST(actingUserID, resourceType, expression)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "cel visual ast failed")
		return nil, appErr
	}
	return result, nil
}
