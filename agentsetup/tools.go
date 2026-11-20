// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// The setup tools, offered only in setup conversations.
const (
	ToolGetMyConfig          = "get_my_config"
	ToolUpdateMyInstructions = "update_my_instructions"
	ToolSetMyDisplayName     = "set_my_display_name"
	ToolDisableMyTools       = "disable_my_tools"
	ToolCompleteSetup        = "complete_setup"
)

// AuditEventAgentSelfSetup is emitted for every setup tool call that writes,
// whether or not the write succeeds. Records carry identifiers and changed
// field names only, never instructions or names.
const AuditEventAgentSelfSetup = "agentSelfSetup"

type updateMyInstructionsArgs struct {
	Instructions string `json:"instructions" jsonschema_description:"Your complete new instructions. They replace your current instructions entirely."`
}

type setMyDisplayNameArgs struct {
	DisplayName string `json:"display_name" jsonschema_description:"The display name people will see for you."`
}

type disableMyToolsArgs struct {
	All         bool                 `json:"all,omitempty" jsonschema_description:"Turn off all of your tools."`
	NativeTools []string             `json:"native_tools,omitempty" jsonschema_description:"Built-in provider tools to turn off, by the names get_my_config lists."`
	MCPTools    []llm.EnabledMCPTool `json:"mcp_tools,omitempty" jsonschema_description:"MCP tools to turn off, exactly as get_my_config lists them."`
}

type emptyArgs struct{}

// selfConfig is what get_my_config returns.
type selfConfig struct {
	DisplayName      string               `json:"display_name"`
	Username         string               `json:"username"`
	Instructions     string               `json:"instructions"`
	ToolsDisabled    bool                 `json:"tools_disabled"`
	NativeTools      []string             `json:"native_tools"`
	AllMCPTools      bool                 `json:"all_mcp_tools"`
	MCPTools         []llm.EnabledMCPTool `json:"mcp_tools"`
	SetupCompleted   bool                 `json:"setup_completed"`
	ChangeableFields []string             `json:"changeable_fields"`
}

func (s *Service) setupTools(agentID string) []llm.Tool {
	return []llm.Tool{
		{
			Name:        ToolGetMyConfig,
			Description: "Read your own current configuration: display name, username, instructions, and which tools you have.",
			Schema:      llm.NewJSONSchemaFromStruct[emptyArgs](),
			// Reading the configuration the requester already manages has no side effect.
			AutoExecute: true,
			Resolver: func(ctx context.Context, llmCtx *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return s.getMyConfig(ctx, requesterID(llmCtx), agentID)
			},
		},
		{
			Name:        ToolUpdateMyInstructions,
			Description: "Replace your own instructions, which every future conversation with you starts from. Show the user the full text and get their confirmation first.",
			Schema:      llm.NewJSONSchemaFromStruct[updateMyInstructionsArgs](),
			Resolver: func(ctx context.Context, llmCtx *llm.Context, getArgs llm.ToolArgumentGetter) (string, error) {
				var args updateMyInstructionsArgs
				if err := getArgs(&args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				return s.write(ctx, ToolUpdateMyInstructions, requesterID(llmCtx), agentID, func(cfg *llm.BotConfig) error {
					cfg.CustomInstructions = args.Instructions
					return nil
				}, "Your instructions are saved. They apply to new conversations with you.")
			},
		},
		{
			Name:        ToolSetMyDisplayName,
			Description: "Change the display name people see for you. Your username does not change.",
			Schema:      llm.NewJSONSchemaFromStruct[setMyDisplayNameArgs](),
			ValidateArguments: func(raw json.RawMessage) error {
				var args setMyDisplayNameArgs
				if err := json.Unmarshal(raw, &args); err != nil {
					return fmt.Errorf("invalid arguments: %w", err)
				}
				_, err := normalizeDisplayName(args.DisplayName)
				return err
			},
			Resolver: func(ctx context.Context, llmCtx *llm.Context, getArgs llm.ToolArgumentGetter) (string, error) {
				var args setMyDisplayNameArgs
				if err := getArgs(&args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				name, err := normalizeDisplayName(args.DisplayName)
				if err != nil {
					return "", err
				}
				return s.write(ctx, ToolSetMyDisplayName, requesterID(llmCtx), agentID, func(cfg *llm.BotConfig) error {
					cfg.DisplayName = name
					return nil
				}, "Your display name is saved.")
			},
		},
		{
			Name:        ToolDisableMyTools,
			Description: "Turn off some or all of your own tools. Only call it when the user asks for tools to be turned off; tools cannot be turned back on in setup.",
			Schema:      llm.NewJSONSchemaFromStruct[disableMyToolsArgs](),
			ValidateArguments: func(raw json.RawMessage) error {
				var args disableMyToolsArgs
				if err := json.Unmarshal(raw, &args); err != nil {
					return fmt.Errorf("invalid arguments: %w", err)
				}
				_, err := normalizeDisableArgs(args)
				return err
			},
			Resolver: func(ctx context.Context, llmCtx *llm.Context, getArgs llm.ToolArgumentGetter) (string, error) {
				var args disableMyToolsArgs
				if err := getArgs(&args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				args, err := normalizeDisableArgs(args)
				if err != nil {
					return "", err
				}
				return s.write(ctx, ToolDisableMyTools, requesterID(llmCtx), agentID, func(cfg *llm.BotConfig) error {
					return disableTools(cfg, args)
				}, "Those tools are turned off for new conversations with you.")
			},
		},
		{
			Name:        ToolCompleteSetup,
			Description: "Mark your setup as complete once the user confirms you are set up.",
			Schema:      llm.NewJSONSchemaFromStruct[emptyArgs](),
			Resolver: func(ctx context.Context, llmCtx *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return s.completeSetup(ctx, requesterID(llmCtx), agentID)
			},
		},
	}
}

func requesterID(llmCtx *llm.Context) string {
	if llmCtx == nil || llmCtx.RequestingUser == nil {
		return ""
	}
	return llmCtx.RequestingUser.Id
}

func normalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("display_name must not be empty")
	}
	if utf8.RuneCountInString(name) > model.BotDisplayNameMaxRunes {
		return "", fmt.Errorf("display_name must be at most %d characters", model.BotDisplayNameMaxRunes)
	}
	return name, nil
}

// errMCPToolsGrantedAll refuses turning off single MCP tools of an agent that
// gets every MCP tool: that would replace the grant with an explicit list,
// which only the agent's editor can do.
var errMCPToolsGrantedAll = errors.New("you get every MCP tool, so single MCP tools cannot be turned off in setup; ask the user to change your MCP tools when they edit you in Agents, or turn off all of your tools")

// normalizeDisableArgs trims and deduplicates the named tools, and rejects an
// empty name or a call that names nothing.
func normalizeDisableArgs(args disableMyToolsArgs) (disableMyToolsArgs, error) {
	out := disableMyToolsArgs{All: args.All}
	for _, name := range args.NativeTools {
		name = strings.TrimSpace(name)
		if name == "" {
			return disableMyToolsArgs{}, errors.New("native_tools must not contain an empty name")
		}
		if !slices.Contains(out.NativeTools, name) {
			out.NativeTools = append(out.NativeTools, name)
		}
	}
	for _, tool := range args.MCPTools {
		tool = llm.EnabledMCPTool{ServerOrigin: strings.TrimSpace(tool.ServerOrigin), ToolName: strings.TrimSpace(tool.ToolName)}
		if tool.ServerOrigin == "" || tool.ToolName == "" {
			return disableMyToolsArgs{}, errors.New("each mcp_tools entry needs a server_origin and a tool_name")
		}
		if !slices.Contains(out.MCPTools, tool) {
			out.MCPTools = append(out.MCPTools, tool)
		}
	}
	if !out.All && len(out.NativeTools) == 0 && len(out.MCPTools) == 0 {
		return disableMyToolsArgs{}, errors.New("name the tools to turn off, or set all")
	}
	return out, nil
}

// disableTools narrows cfg's tools as args asks. It replaces the tool slices
// rather than editing them, because cfg shares them with the stored agent.
func disableTools(cfg *llm.BotConfig, args disableMyToolsArgs) error {
	if args.All {
		cfg.DisableTools = true
	}
	if len(args.NativeTools) > 0 {
		cfg.EnabledNativeTools = slices.DeleteFunc(slices.Clone(cfg.EnabledNativeTools), func(name string) bool {
			return slices.Contains(args.NativeTools, name)
		})
	}
	if len(args.MCPTools) > 0 {
		if cfg.AutoEnableNewMCPTools {
			return errMCPToolsGrantedAll
		}
		cfg.EnabledMCPTools = slices.DeleteFunc(slices.Clone(cfg.EnabledMCPTools), func(tool llm.EnabledMCPTool) bool {
			return slices.Contains(args.MCPTools, tool)
		})
	}
	return nil
}

func (s *Service) getMyConfig(ctx context.Context, actorID, agentID string) (string, error) {
	agent, err := s.deps.Configs.Get(ctx, actorID, agentID)
	if err != nil {
		return "", toolError(err)
	}
	out := selfConfig{
		DisplayName:      agent.DisplayName,
		Username:         agent.Name,
		Instructions:     agent.CustomInstructions,
		ToolsDisabled:    agent.DisableTools,
		NativeTools:      nonNil(agent.EnabledNativeTools),
		AllMCPTools:      agent.AutoEnableNewMCPTools,
		MCPTools:         nonNil(agent.EnabledMCPTools),
		SetupCompleted:   agent.SetupCompletedAt != 0,
		ChangeableFields: agentconfig.SelfSetupFields,
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("failed to encode configuration: %w", err)
	}
	return string(data), nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// write saves apply as a self_setup version on behalf of actorID. agentconfig
// re-authorizes the actor and refuses fields outside SelfSetupFields.
func (s *Service) write(ctx context.Context, toolName, actorID, agentID string, apply func(*llm.BotConfig) error, success string) (string, error) {
	rec := s.newAuditRecord(toolName, actorID, agentID)
	defer s.deps.Audit.Record(rec)

	result, err := s.deps.Configs.Update(ctx, agentconfig.UpdateRequest{
		ActorID: actorID,
		AgentID: agentID,
		Apply:   apply,
		Meta:    store.AgentVersionMeta{ActorID: actorID, Source: store.AgentVersionSourceSelfSetup},
	})
	if err != nil {
		rec.AddErrorDesc(kindName(agentconfig.KindOf(err)))
		return "", toolError(err)
	}
	audit.AddParam(rec, "changed_fields", result.ChangedFields)
	rec.Success()
	if len(result.ChangedFields) == 0 {
		return "Nothing changed: that is already your configuration.", nil
	}
	return success + " The user can restore the previous version from your history.", nil
}

func (s *Service) completeSetup(ctx context.Context, actorID, agentID string) (string, error) {
	rec := s.newAuditRecord(ToolCompleteSetup, actorID, agentID)
	defer s.deps.Audit.Record(rec)

	if _, err := s.deps.Configs.Get(ctx, actorID, agentID); err != nil {
		rec.AddErrorDesc(kindName(agentconfig.KindOf(err)))
		return "", toolError(err)
	}
	marked, err := s.deps.Store.MarkAgentSetupCompleted(agentID, model.GetMillis())
	if err != nil {
		rec.AddErrorDesc(kindName(agentconfig.KindInternal))
		return "", errors.New("failed to record that setup is complete")
	}
	audit.AddParam(rec, "newly_completed", marked)
	rec.Success()
	return "Setup is complete.", nil
}

func (s *Service) newAuditRecord(toolName, actorID, agentID string) *model.AuditRecord {
	rec := plugin.MakeAuditRecord(AuditEventAgentSelfSetup, model.AuditStatusFail)
	rec.Actor.UserId = actorID
	audit.AddParam(rec, audit.KeyAgentID, agentID)
	audit.AddParam(rec, audit.KeyToolName, toolName)
	return rec
}

// toolError turns an agentconfig error into a tool result the model can act
// on. Internal failures are not described.
func toolError(err error) error {
	switch agentconfig.KindOf(err) {
	case agentconfig.KindInternal, agentconfig.KindPolicy:
		return errors.New("the change could not be saved because of a server error; ask the user to try again later")
	case agentconfig.KindForbidden, agentconfig.KindNotFound:
		return fmt.Errorf("not allowed: %w", err)
	default:
		return err
	}
}

func kindName(kind agentconfig.Kind) string {
	switch kind {
	case agentconfig.KindNotFound:
		return "not_found"
	case agentconfig.KindForbidden:
		return "forbidden"
	case agentconfig.KindInvalid:
		return "invalid"
	case agentconfig.KindConflict:
		return "conflict"
	case agentconfig.KindNotLicensed:
		return "not_licensed"
	case agentconfig.KindAccess:
		return "access_denied"
	case agentconfig.KindPolicy:
		return "policy"
	default:
		return "internal"
	}
}
