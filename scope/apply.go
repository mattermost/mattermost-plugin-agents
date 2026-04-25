// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package scope implements scoped, stateless agent runs driven by subscriptions
// and schedules. A scoped run executes the LLM with a reduced tool set whose
// arguments (notably channel_id) are pre-bound so the LLM cannot act outside
// the trigger's configured target.
package scope

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// Logger is the minimal logging surface used inside scoped runs. It matches
// the shape of pluginapi.LogService so any pluginapi.Client can be passed in.
type Logger interface {
	Error(message string, keyValuePairs ...any)
	Warn(message string, keyValuePairs ...any)
	Info(message string, keyValuePairs ...any)
}

// ApplyToolScope returns a fresh *llm.ToolStore that exposes only the tools
// the trigger allows, with BoundParams substituted and wired through
// Tool.WithBoundParams. It does not mutate the input store.
//
// The {{TargetChannelID}} sentinel in any bound param value is swapped for
// targetChannelID, letting the webapp write a template-like value and the
// runtime resolve it per fire.
func ApplyToolScope(
	source *llm.ToolStore,
	allowedTools []string,
	boundParams map[string]map[string]interface{},
	targetChannelID string,
	log llm.TraceLog,
) *llm.ToolStore {
	return ApplyToolScopeWithTarget(source, allowedTools, boundParams, targetChannelID, nil, nil, log)
}

// ApplyToolScopeWithTarget is ApplyToolScope plus server-known channel/team
// context. Scoped runs bind these verification fields so a trigger can allow
// only create_post while still satisfying that tool's safety checks.
func ApplyToolScopeWithTarget(
	source *llm.ToolStore,
	allowedTools []string,
	boundParams map[string]map[string]interface{},
	targetChannelID string,
	targetChannel *model.Channel,
	targetTeam *model.Team,
	log llm.TraceLog,
) *llm.ToolStore {
	scoped := llm.NewToolStore(log, false)
	if source == nil || len(allowedTools) == 0 {
		return scoped
	}

	allowed := make(map[string]struct{}, len(allowedTools))
	for _, name := range allowedTools {
		allowed[name] = struct{}{}
	}

	keep := make([]llm.Tool, 0, len(allowedTools))
	for _, tool := range source.GetTools() {
		if _, ok := allowed[tool.Name]; !ok {
			continue
		}

		params := resolveBoundParams(boundParams[tool.Name], targetChannelID)
		params = addTargetContextBoundParams(tool.Name, params, targetChannelID, targetChannel, targetTeam)
		if len(params) > 0 {
			tool = tool.WithBoundParams(params)
		}
		if tool.Name == "create_post" {
			tool.Description = "Create a new post in the configured target channel. The system has already fixed and verified channel_id, channel_display_name, and team_display_name; provide only the message content and optional root_id/attachments."
		}
		keep = append(keep, tool)
	}
	scoped.AddTools(keep)
	return scoped
}

// resolveBoundParams substitutes sentinel values in a bound-params map.
// Callers receive a new map; the input is never mutated.
func resolveBoundParams(in map[string]interface{}, targetChannelID string) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok && s == llm.BoundParamTargetChannelSentinel {
			out[k] = targetChannelID
			continue
		}
		out[k] = v
	}
	return out
}

func addTargetContextBoundParams(toolName string, params map[string]interface{}, targetChannelID string, targetChannel *model.Channel, targetTeam *model.Team) map[string]interface{} {
	if toolName != "create_post" {
		return params
	}
	if len(params) == 0 {
		params = map[string]interface{}{}
	}
	if _, ok := params["channel_id"]; !ok && targetChannelID != "" {
		params["channel_id"] = targetChannelID
	}
	if _, ok := params["channel_display_name"]; !ok && targetChannel != nil {
		params["channel_display_name"] = targetChannel.DisplayName
	}
	if _, ok := params["team_display_name"]; !ok && targetTeam != nil {
		params["team_display_name"] = targetTeam.DisplayName
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

// AssertBoundParams logs an ERROR for every (tool, param) in the triggers'
// BoundParams that cannot be wired — either because the tool is not in the
// store, or because its schema has no such property. Callers should call this
// at activation after bots are ready; it is defensive and never returns an
// error (a misbound param degrades to "tool receives empty value and fails"
// rather than a security hole, since the schema-strip prevents the LLM from
// providing its own value).
func AssertBoundParams(
	source *llm.ToolStore,
	subs []llm.AgentSubscription,
	scheds []llm.AgentSchedule,
	log Logger,
) {
	if log == nil {
		return
	}
	for i := range subs {
		assertTriggerBoundParams(source, subs[i].BoundParams, fmt.Sprintf("subscription[%s]", subs[i].ID), log)
	}
	for i := range scheds {
		assertTriggerBoundParams(source, scheds[i].BoundParams, fmt.Sprintf("schedule[%s]", scheds[i].ID), log)
	}
}

func assertTriggerBoundParams(source *llm.ToolStore, bound map[string]map[string]interface{}, label string, log Logger) {
	for toolName, params := range bound {
		tool := source.GetTool(toolName)
		if tool == nil {
			log.Error("scope: bound-params tool not found", "trigger", label, "tool", toolName)
			continue
		}
		schema, ok := tool.Schema.(*jsonschema.Schema)
		if !ok || schema == nil {
			log.Warn("scope: tool schema not introspectable, skipping bound-params assertion", "trigger", label, "tool", toolName)
			continue
		}
		for paramName := range params {
			if _, exists := schema.Properties[paramName]; !exists {
				log.Error("scope: bound-params property missing on tool schema", "trigger", label, "tool", toolName, "param", paramName)
			}
		}
	}
}
