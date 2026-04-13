// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
)

// MattermostScopeSchemaExtraKey is the jsonschema.Schema.Extra key used to mark
// parameters that participate in MattermostAccessScope binding (not sent to LLM JSON).
const MattermostScopeSchemaExtraKey = "x-mattermost-scope"

// Scope tag values (struct tag `scope:"..."`) mirrored into schema Extra.
const (
	MattermostScopeTagTeamID    = "team_id"
	MattermostScopeTagChannelID = "channel_id"
)

// ScopeTaggedParams returns JSON property names marked with Mattermost scope tags
// on the root object schema (via Schema.Extra on each property subschema).
func ScopeTaggedParams(schema any) (teamID, channelID []string) {
	root, ok := schema.(*jsonschema.Schema)
	if !ok || root == nil || root.Properties == nil {
		return nil, nil
	}
	for name, prop := range root.Properties {
		if prop == nil || prop.Extra == nil {
			continue
		}
		v, ok := prop.Extra[MattermostScopeSchemaExtraKey].(string)
		if !ok {
			continue
		}
		switch v {
		case MattermostScopeTagTeamID:
			teamID = append(teamID, name)
		case MattermostScopeTagChannelID:
			channelID = append(channelID, name)
		}
	}
	return teamID, channelID
}

// ValidateMattermostAccessScopeArgs rejects scoped argument values that fall outside
// the runtime MattermostAccessScope for the current run.
func ValidateMattermostAccessScopeArgs(schema any, scope *MattermostAccessScope, rawArgs map[string]any) error {
	if scope == nil {
		return nil
	}

	teamParams, channelParams := ScopeTaggedParams(schema)
	if err := validateRequiredScopedParams(schema, append(slices.Clone(teamParams), channelParams...), rawArgs); err != nil {
		return err
	}
	if err := validateScopeStringParams(rawArgs, teamParams, func(value string) bool {
		return scope.TeamID == "" || value == scope.TeamID
	}); err != nil {
		return err
	}

	if err := validateScopeStringParams(rawArgs, channelParams, func(value string) bool {
		return len(scope.AllowedChannelIDs) == 0 || slices.Contains(scope.AllowedChannelIDs, value)
	}); err != nil {
		return err
	}

	return nil
}

func validateRequiredScopedParams(schema any, params []string, rawArgs map[string]any) error {
	root, ok := schema.(*jsonschema.Schema)
	if !ok || root == nil || len(root.Required) == 0 || len(params) == 0 {
		return nil
	}

	for _, param := range params {
		if !slices.Contains(root.Required, param) {
			continue
		}
		if _, ok := rawArgs[param]; !ok {
			return fmt.Errorf("field %q is required by the execution scope for this run", param)
		}
	}

	return nil
}

func validateScopeStringParams(rawArgs map[string]any, params []string, allowed func(string) bool) error {
	for _, param := range params {
		rawValue, ok := rawArgs[param]
		if !ok {
			continue
		}

		value, ok := rawValue.(string)
		if !ok {
			return fmt.Errorf("field %q must be a string", param)
		}
		if !allowed(value) {
			return fmt.Errorf("field %q value %q is outside the execution scope for this run", param, value)
		}
	}

	return nil
}

// WithConstrainedParams sets enum and required on the given JSON property names.
// Used when the LLM must choose one of several allowed string values (e.g. channel IDs).
func (t Tool) WithConstrainedParams(constraints map[string][]string) Tool {
	if len(constraints) == 0 {
		return t
	}
	root, ok := t.Schema.(*jsonschema.Schema)
	if !ok || root == nil {
		return t
	}
	cloned := root.CloneSchemas()
	if cloned.Properties == nil {
		return t
	}
	for param, values := range constraints {
		if len(values) == 0 {
			continue
		}
		prop, ok := cloned.Properties[param]
		if !ok || prop == nil {
			continue
		}
		p := *prop
		enumVals := make([]any, len(values))
		for i, v := range values {
			enumVals[i] = v
		}
		p.Enum = enumVals
		if len(prop.Extra) > 0 {
			p.Extra = maps.Clone(prop.Extra)
		}
		cloned.Properties[param] = &p
		if !slices.Contains(cloned.Required, param) {
			cloned.Required = append(slices.Clone(cloned.Required), param)
		}
	}
	return Tool{
		Name:         t.Name,
		Description:  t.Description,
		Schema:       cloned,
		Resolver:     t.Resolver,
		ServerOrigin: t.ServerOrigin,
	}
}

// ApplyMattermostAccessScope rewrites tools in the store: binds team_id and optionally
// channel_id per scope tags on each tool schema (Extra[MattermostScopeSchemaExtraKey]).
// It is a no-op if scope is nil or the store is nil.
func ApplyMattermostAccessScope(store *ToolStore, scope *MattermostAccessScope) {
	if store == nil || scope == nil {
		return
	}
	newTools := make(map[string]Tool, len(store.tools))
	for name, tool := range store.tools {
		newTools[name] = applyScopeToTool(tool, scope, store.log)
	}
	store.tools = newTools
	if store.log != nil {
		names := make([]string, 0, len(store.tools))
		for n := range store.tools {
			names = append(names, n)
		}
		sort.Strings(names)
		store.log.Info("scope: mattermost access scope applied to tool store", "tool_count", len(names), "tools", names)
	}
}

func applyScopeToTool(tool Tool, scope *MattermostAccessScope, log TraceLog) Tool {
	teamParams, channelParams := ScopeTaggedParams(tool.Schema)
	t := tool

	if scope.TeamID != "" && len(teamParams) > 0 {
		bind := make(map[string]interface{}, len(teamParams))
		for _, p := range teamParams {
			bind[p] = scope.TeamID
		}
		t = t.WithBoundParams(bind)
		if log != nil {
			log.Info("scope: bound team_id parameters", "tool", t.Name, "params", teamParams)
		}
	}

	if len(channelParams) > 0 {
		ids := scope.AllowedChannelIDs
		switch len(ids) {
		case 0:
			// no channel allowlist: leave channel_id optional
		case 1:
			bind := make(map[string]interface{}, len(channelParams))
			for _, p := range channelParams {
				bind[p] = ids[0]
			}
			t = t.WithBoundParams(bind)
			if log != nil {
				log.Info("scope: bound channel_id parameters", "tool", t.Name, "params", channelParams)
			}
		default:
			constraints := make(map[string][]string, len(channelParams))
			for _, p := range channelParams {
				constraints[p] = slices.Clone(ids)
			}
			t = t.WithConstrainedParams(constraints)
			if log != nil {
				log.Info("scope: constrained channel_id to enum+required", "tool", t.Name, "params", channelParams)
			}
		}
	}

	return t
}
