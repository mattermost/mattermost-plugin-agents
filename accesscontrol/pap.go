// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"fmt"
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
// package — so a successful save/delete can never skip the policy-index
// update, and the api package never holds a raw plugin.API handle.

func isNotFoundAppErr(appErr *model.AppError) bool {
	return appErr != nil && appErr.StatusCode == http.StatusNotFound
}

// SavePolicy overwrites the identity fields (ID, Type, Version, Active) per
// contract §7.2, defaults an empty Name to defaultName, and persists the
// policy with conservative ordering under the policy-index mutex: the
// fail-closed index marker is written FIRST, then the policy. An enforced
// policy therefore can never exist without its outage marker; the inverse
// (a marker without a policy, after a failed save) only fails closed during
// ABAC outages — acceptable. On save failure the marker this call added is
// removed best-effort; a failed removal is tolerated for the same reason.
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

	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()

	// Remember whether the marker pre-existed so a rollback below can never
	// strip the marker of a still-enforced older policy.
	hadMarker, hasErr := c.index.Has(resourceType, resourceID)

	if err := c.index.Add(resourceType, resourceID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "policy index update failed")
		return nil, fmt.Errorf("failed to update the policy index; the policy was not saved: %w", err)
	}

	saved, appErr := c.papi.SaveAccessControlPolicy(actingUserID, policy)
	if appErr != nil {
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "policy save failed")
		if hasErr == nil && !hadMarker {
			if rmErr := c.index.Remove(resourceType, resourceID); rmErr != nil {
				logWarn(c.log, "Policy save failed and the policy index marker could not be rolled back; the stale marker fails closed during ABAC outages",
					"resource_type", resourceType, "resource_id", resourceID, "error", rmErr.Error())
			}
		}
		return nil, appErr
	}
	return saved, nil
}

// GetPolicy returns the stored policy or ErrPolicyNotFound. Ownership/type
// scoping is enforced server-side (contract §4.1).
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

// DeletePolicy deletes the stored policy and clears its policy-index marker,
// under the policy-index mutex and in that order: a marker-removal failure
// after a successful delete leaves a stale fail-closed marker — acceptable
// (contract risk #3) and logged. Returns ErrPolicyNotFound when no policy
// exists.
//
// Before dropping the marker the delete is confirmed against server truth
// (Get returning 404): the mutex lease is renewable but not fenced, so a
// save from an instance whose lease was lost could re-create the policy
// between our delete and the marker removal. When the confirm fails or finds
// a policy, the marker is kept — a stale marker only fails closed during
// ABAC outages and self-heals at the next decision (see reconcileIndex).
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

	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()

	if appErr := c.papi.DeleteAccessControlPolicy(actingUserID, resourceType, resourceID); appErr != nil {
		if isNotFoundAppErr(appErr) {
			return ErrPolicyNotFound
		}
		span.RecordError(appErr)
		span.SetStatus(codes.Error, "policy delete failed")
		return appErr
	}

	// Confirm the policy is really gone before dropping the fail-closed
	// marker; keep the marker whenever the 404 cannot be confirmed.
	if _, appErr := c.papi.GetAccessControlPolicy(resourceID); !isNotFoundAppErr(appErr) {
		logWarn(c.log, "Policy delete could not be confirmed against server truth; keeping the fail-closed marker",
			"resource_type", resourceType, "resource_id", resourceID)
		return nil
	}

	if err := c.index.Remove(resourceType, resourceID); err != nil {
		// Stale marker only fails closed during ABAC outages — acceptable
		// (contract risk #3); the delete itself succeeded.
		logError(c.log, "Policy deleted but policy index cleanup failed; a stale fail-closed marker remains",
			"resource_type", resourceType, "resource_id", resourceID, "error", err.Error())
		span.RecordError(err)
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
