// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package skills lists the skills an agent may load for a request and
// provides the load_skill and refresh_skills tools that load them.
package skills

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"go.opentelemetry.io/otel/trace"
)

const (
	LoadSkillToolName     = "load_skill"
	RefreshSkillsToolName = "refresh_skills"
)

// builtinSkill is a skill shipped with the plugin. Its ID is namespaced so no
// user skill ID can match it, and it is listed only when the tool it needs is
// in the request's catalog, loaded or not.
type builtinSkill struct {
	id           string
	name         string
	description  string
	template     string
	requiresTool string
}

var builtinSkills = []builtinSkill{
	{
		id:           "mattermost/summarize-conversations",
		name:         "Summarize channels and threads",
		description:  "Use when the user asks to summarize, recap, or catch up on a Mattermost channel, thread, or time range.",
		template:     prompts.PromptSkillSummarizeConversations,
		requiresTool: "read_channel",
	},
}

// PromptStore lists the user-created skills visible to a user.
type PromptStore interface {
	ListForUser(userID string, includeShared bool) ([]customprompts.CustomPrompt, error)
}

type Provider struct {
	store         PromptStore
	prompts       *llm.Prompts
	sharedAllowed func() bool
}

// NewProvider returns a skill provider. sharedAllowed reports whether the
// license allows shared skills; it is read on every request.
func NewProvider(store PromptStore, prompts *llm.Prompts, sharedAllowed func() bool) *Provider {
	return &Provider{store: store, prompts: prompts, sharedAllowed: sharedAllowed}
}

type skill struct {
	info   llm.SkillInfo
	render func(*llm.Context) (string, error)
}

// Tools returns the skill tools and the catalog they serve for a request to
// bot, or nothing when no skill is available.
func (p *Provider) Tools(bot llm.BotConfig, c *llm.Context) ([]llm.Tool, []llm.SkillInfo, error) {
	available, err := p.available(bot, c)
	if err != nil || len(available) == 0 {
		return nil, nil, err
	}
	return []llm.Tool{p.loadSkillTool(bot), p.refreshSkillsTool(bot)}, catalog(available), nil
}

// available lists the skills bot may load for c's requester, from current
// settings and access. Personal skills are listed only in the requester's DM
// with the agent, where the requester is the only human reader.
func (p *Provider) available(bot llm.BotConfig, c *llm.Context) ([]skill, error) {
	if bot.Skills.Disabled || c == nil || c.RequestingUser == nil {
		return nil, nil
	}

	var out []skill
	for _, b := range builtinSkills {
		if !hasTool(c.Tools, b.requiresTool) {
			continue
		}
		out = append(out, skill{
			info: llm.SkillInfo{ID: b.id, Name: b.name, Description: b.description},
			render: func(c *llm.Context) (string, error) {
				return p.prompts.Format(b.template, c)
			},
		})
	}

	userID := c.RequestingUser.Id
	personal := bot.Skills.AllowPersonal && mmapi.IsDMWith(c.BotUserID, c.Channel)
	shared := len(bot.Skills.EnabledSharedIDs) > 0 && p.sharedAllowed != nil && p.sharedAllowed()
	if p.store == nil || (!personal && !shared) {
		return out, nil
	}

	userPrompts, err := p.store.ListForUser(userID, shared)
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	for _, prompt := range userPrompts {
		isPersonal := personal && prompt.CreatorID == userID
		isShared := shared && prompt.IsShared && slices.Contains(bot.Skills.EnabledSharedIDs, prompt.ID)
		if !isPersonal && !isShared {
			continue
		}
		description := prompt.Description
		if override := strings.TrimSpace(bot.Skills.DescriptionOverrides[prompt.ID]); override != "" {
			description = override
		}
		description = promptSafeText(description, llm.MaxSkillDescriptionRunes)
		if description == "" {
			continue
		}
		template := prompt.Template
		out = append(out, skill{
			info: llm.SkillInfo{
				ID:          prompt.ID,
				Name:        promptSafeText(prompt.Name, customprompts.MaxPromptNameLength),
				Description: description,
			},
			render: func(c *llm.Context) (string, error) {
				return p.prompts.FormatString(template, c.CustomPromptVars())
			},
		})
	}
	return out, nil
}

type loadSkillArgs struct {
	ID string `json:"id" jsonschema:"The id of the skill to load, exactly as listed,minLength=1"`
}

func (p *Provider) loadSkillTool(bot llm.BotConfig) llm.Tool {
	return llm.Tool{
		Name:        LoadSkillToolName,
		Description: "Load a skill's instructions by id. Call it before working on a request that matches a listed skill's description, then follow the instructions it returns.",
		Schema:      llm.NewJSONSchemaFromStruct[loadSkillArgs](),
		AutoExecute: true,
		Resolver: func(ctx context.Context, llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			var args loadSkillArgs
			if err := argsGetter(&args); err != nil {
				return "", err
			}
			available, err := p.available(bot, llmContext)
			if err != nil {
				return "", err
			}
			id := strings.TrimSpace(args.ID)
			idx := slices.IndexFunc(available, func(s skill) bool { return s.info.ID == id })
			if idx < 0 {
				return "No skill with that id is available. " + describeCatalog(available), nil
			}
			found := available[idx]
			body, err := found.render(llmContext)
			if err != nil {
				return fmt.Sprintf("The %q skill could not be loaded because its instructions failed to render.", found.info.Name), nil
			}
			trace.SpanFromContext(ctx).SetAttributes(telemetry.SkillID.String(found.info.ID))
			return fmt.Sprintf("Instructions for the %q skill follow. Apply them to the user's request.\n\n%s", found.info.Name, body), nil
		},
	}
}

func (p *Provider) refreshSkillsTool(bot llm.BotConfig) llm.Tool {
	return llm.Tool{
		Name:        RefreshSkillsToolName,
		Description: "List the skills currently available. Call it when the user mentions a skill that is not listed, or says they added or changed one.",
		Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
		AutoExecute: true,
		Resolver: func(_ context.Context, llmContext *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			available, err := p.available(bot, llmContext)
			if err != nil {
				return "", err
			}
			return describeCatalog(available), nil
		},
	}
}

func describeCatalog(available []skill) string {
	if len(available) == 0 {
		return "No skills are currently available."
	}
	return "The skills currently available are:\n" + llm.FormatSkillCatalog(catalog(available))
}

func catalog(available []skill) []llm.SkillInfo {
	infos := make([]llm.SkillInfo, len(available))
	for i, s := range available {
		infos[i] = s.info
	}
	return infos
}

func hasTool(store *llm.ToolStore, name string) bool {
	return store != nil && (store.GetTool(name) != nil || store.IsUnloadedMCPTool(name))
}

// promptSafeText flattens user-authored text onto one line and caps it, so it
// cannot break out of its catalog entry.
func promptSafeText(s string, maxRunes int) string {
	s = llm.EscapePromptContent(strings.Join(strings.Fields(llm.SanitizeNonPrintableChars(s)), " "))
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}
