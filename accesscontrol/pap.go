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

// ErrPolicyNotFound is returned when no plugin-owned policy is stored under the resource ID.
var ErrPolicyNotFound = errors.New("access policy not found")

// errNoPluginAPI is returned by mutating/query PAP methods when papi is nil;
// reads report not-found.
var errNoPluginAPI = errors.New("access control plugin API is not available")

// PAP (policy administration) proxying lives on the Checker — not in the api
// package — so the api package never holds a raw plugin.API handle.

func isNotFoundAppErr(appErr *model.AppError) bool {
	return appErr != nil && appErr.StatusCode == http.StatusNotFound
}

// SavePolicy overwrites ID/Type/Version/Active from the route and persists in
// one plugin-API call — the server owns policy existence.
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

// requesterMatchQueryLimit is the page size for RequesterMatchesExpression.
// Usernames are unique; a small page is enough to see whether the acting user
// is in the username-filtered match set.
const requesterMatchQueryLimit = 10

// RequesterMatchesExpression is the plugin-API stand-in for core's
// ValidateExpressionAgainstRequester. The pinned plugin surface exposes
// QueryUsersForAccessControlExpression but not SubjectID or
// ExcludeNativeAttributes, so this probes with the acting user's username.
// Unlike Channel Settings, native-only rules (e.g. email verified) are not
// skipped here and may fail self-inclusion where core would pass.
func (c *Checker) RequesterMatchesExpression(ctx context.Context, actingUserID, resourceType, expression string) (bool, error) {
	_, span := telemetry.Tracer().Start(ctx, "abac cel_test_self_match", trace.WithAttributes(
		telemetry.UserID.String(actingUserID),
		telemetry.ABACResourceType.String(resourceType),
	))
	defer span.End()

	if c.papi == nil {
		return false, errNoPluginAPI
	}

	user, appErr := c.papi.GetUser(actingUserID)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "requester lookup failed")
		return false, appErr
	}
	if user == nil || user.Username == "" {
		return false, nil
	}

	result, appErr := c.papi.QueryUsersForAccessControlExpression(actingUserID, resourceType, expression, user.Username, "", requesterMatchQueryLimit)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "requester match query failed")
		return false, appErr
	}
	if result == nil {
		return false, nil
	}
	for _, matched := range result.Users {
		if matched != nil && matched.Id == actingUserID {
			return true, nil
		}
	}
	return false, nil
}

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
