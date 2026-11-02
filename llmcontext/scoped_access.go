// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"cmp"
	stdcontext "context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// maxScopedAccessTeamsListed bounds each team list in a ScopedAccessReport;
// the counts always cover every team.
const maxScopedAccessTeamsListed = 100

// ScopedToolRemoval says why a tool the agent's allowlist names is not in its
// scoped catalog. The values of mcp.ScopedToolExclusion are used as they are.
type ScopedToolRemoval string

const (
	// ScopedRemovedNoServiceAccount: the remote server has no service-account
	// credentials, which scoped runs need.
	ScopedRemovedNoServiceAccount ScopedToolRemoval = "no_service_account"
	// ScopedRemovedUnavailable: the server or tool could not be found when the
	// catalog was built.
	ScopedRemovedUnavailable ScopedToolRemoval = "unavailable"
	// ScopedRemovedNotOptedIn: a built-in tool that reaches outside Mattermost
	// and that the allowlist does not name.
	ScopedRemovedNotOptedIn ScopedToolRemoval = "not_opted_in"
)

// Statuses of an extra channel in a ScopedAccessReport.
const (
	ScopedExtraChannelReadable        = "readable"
	ScopedExtraChannelMissing         = "missing"
	ScopedExtraChannelArchived        = "archived"
	ScopedExtraChannelRestricted      = "restricted"
	ScopedExtraChannelUnsupportedType = "unsupported_type"
)

// ScopedAccessReport describes what an agent's scoped channel runs can access.
// It is computed from the rules the runtime applies, not described separately.
type ScopedAccessReport struct {
	// Configured is whether the agent has scoped channel mode turned on.
	Configured bool `json:"configured"`
	// Active is whether it can take effect right now; Blockers says why not.
	Active   bool                       `json:"active"`
	Blockers []ScopedChannelModeBlocker `json:"blockers"`
	Channels ScopedAccessChannels       `json:"channels"`
	Tools    []ScopedAccessTool         `json:"tools"`
}

type ScopedAccessChannels struct {
	// TriggeringTeamPublicChannels is whether every run can read all public
	// channels on the team of the channel it was triggered in.
	TriggeringTeamPublicChannels bool                  `json:"triggeringTeamPublicChannels"`
	OpenTeams                    ScopedAccessOpenTeams `json:"openTeams"`
	ExtraChannels                []ScopedAccessChannel `json:"extraChannels"`
}

// ScopedAccessOpenTeams summarizes the grant to every public channel on an
// open team.
type ScopedAccessOpenTeams struct {
	Enabled        bool                           `json:"enabled"`
	DisabledReason scopedsession.OpenTeamGrantOff `json:"disabledReason,omitempty"`
	OpenCount      int                            `json:"openCount"`
	ClosedCount    int                            `json:"closedCount"`
	Open           []ScopedAccessTeam             `json:"open"`
	Closed         []ScopedAccessClosedTeam       `json:"closed"`
}

type ScopedAccessTeam struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type ScopedAccessClosedTeam struct {
	ScopedAccessTeam
	Reasons []scopedsession.TeamClosedReason `json:"reasons"`
}

type ScopedAccessChannel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	TeamName    string `json:"teamName,omitempty"`
	Type        string `json:"type,omitempty"`
	Status      string `json:"status"`
}

// ScopedAccessTool is one tool the agent's allowlist names, or a built-in tool
// the agent could opt into, and whether scoped runs get it.
type ScopedAccessTool struct {
	ServerOrigin string `json:"serverOrigin"`
	ServerName   string `json:"serverName,omitempty"`
	ServerKind   string `json:"serverKind"`
	// Name is the tool's name, or "*" for an entry granting every tool on the server.
	Name    string `json:"name"`
	Offered bool   `json:"offered"`
	// Reason is why the tool was removed; empty when offered.
	Reason ScopedToolRemoval `json:"reason,omitempty"`
}

// ScopedServerSummarizer describes the MCP servers behind a catalog.
type ScopedServerSummarizer interface {
	ServerSummary(origin string) (mcp.ServerSummary, bool)
}

var _ ScopedServerSummarizer = (*mcp.ClientManager)(nil)

type pluginServerSettings struct{ client *pluginapi.Client }

func (s pluginServerSettings) GetConfig() *model.Config { return s.client.Configuration.GetConfig() }

func (s pluginServerSettings) RolesGrantPermission(roleNames []string, permissionID string) bool {
	return s.client.User.RolesGrantPermission(roleNames, permissionID)
}

// ScopedAccessReport computes what bot's scoped channel runs can access.
// viewerID is the administrator asking: the tool catalog is built for them, so
// per-user MCP access policy of the person who triggers a run can still narrow
// it further.
func (b *Builder) ScopedAccessReport(ctx stdcontext.Context, bot *bots.Bot, viewerID string) (*ScopedAccessReport, error) {
	if bot == nil {
		return nil, errors.New("agent has no bot")
	}
	report := &ScopedAccessReport{
		Configured: ScopedChannelModeConfigured(bot),
		Active:     b.ScopedChannelModeAvailable(bot),
		Blockers:   b.ScopedChannelModeBlockers(bot),
	}
	report.Channels.TriggeringTeamPublicChannels = true
	if report.Blockers == nil {
		report.Blockers = []ScopedChannelModeBlocker{}
	}

	openTeams, err := b.scopedOpenTeams()
	if err != nil {
		return nil, err
	}
	report.Channels.OpenTeams = openTeams

	extras, err := b.scopedExtraChannels(bot.GetConfig().ScopedExtraChannelIDs)
	if err != nil {
		return nil, err
	}
	report.Channels.ExtraChannels = extras

	tools, err := b.scopedAccessTools(ctx, bot, viewerID)
	if err != nil {
		return nil, err
	}
	report.Tools = tools
	return report, nil
}

func (b *Builder) scopedOpenTeams() (ScopedAccessOpenTeams, error) {
	result := ScopedAccessOpenTeams{
		DisabledReason: scopedsession.OpenTeamGrantOffReason(pluginServerSettings{b.pluginAPI}),
		Open:           []ScopedAccessTeam{},
		Closed:         []ScopedAccessClosedTeam{},
	}
	result.Enabled = result.DisabledReason == scopedsession.OpenTeamGrantOn

	teams, err := b.pluginAPI.Team.List()
	if err != nil {
		return result, fmt.Errorf("failed to list teams: %w", err)
	}
	teams = slices.Clone(teams)
	slices.SortFunc(teams, func(a, c *model.Team) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(c.DisplayName)), cmp.Compare(a.Id, c.Id))
	})
	for _, team := range teams {
		entry := ScopedAccessTeam{ID: team.Id, DisplayName: team.DisplayName}
		reasons := scopedsession.TeamClosedReasons(team)
		switch {
		case !result.Enabled:
			result.ClosedCount++
		case len(reasons) == 0:
			result.OpenCount++
			if len(result.Open) < maxScopedAccessTeamsListed {
				result.Open = append(result.Open, entry)
			}
		default:
			result.ClosedCount++
			if len(result.Closed) < maxScopedAccessTeamsListed {
				result.Closed = append(result.Closed, ScopedAccessClosedTeam{ScopedAccessTeam: entry, Reasons: reasons})
			}
		}
	}
	return result, nil
}

func (b *Builder) scopedExtraChannels(channelIDs []string) ([]ScopedAccessChannel, error) {
	extras := make([]ScopedAccessChannel, 0, len(channelIDs))
	for _, id := range channelIDs {
		channel, err := b.pluginAPI.Channel.Get(id)
		if errors.Is(err, pluginapi.ErrNotFound) {
			extras = append(extras, ScopedAccessChannel{ID: id, Status: ScopedExtraChannelMissing})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to get scoped extra channel: %w", err)
		}

		entry := ScopedAccessChannel{ID: id, DisplayName: channel.DisplayName, Type: string(channel.Type)}
		if channel.TeamId != "" {
			team, teamErr := b.pluginAPI.Team.Get(channel.TeamId)
			if teamErr != nil {
				return nil, fmt.Errorf("failed to get scoped extra channel's team: %w", teamErr)
			}
			entry.TeamName = team.DisplayName
		}

		switch {
		case model.SessionScopeAllowsChannel(scopedsession.NewRunScope("", []string{id}), channel, nil, true):
			entry.Status = ScopedExtraChannelReadable
		case channel.DeleteAt != 0:
			entry.Status = ScopedExtraChannelArchived
		case channel.IsGroupOrDirect():
			entry.Status = ScopedExtraChannelUnsupportedType
		default:
			entry.Status = ScopedExtraChannelRestricted
		}
		extras = append(extras, entry)
	}
	return extras, nil
}

func (b *Builder) scopedAccessTools(ctx stdcontext.Context, bot *bots.Bot, viewerID string) ([]ScopedAccessTool, error) {
	cfg := bot.GetConfig()
	allowlist := cfg.EnabledMCPTools
	tools := []ScopedAccessTool{}

	var catalog []llm.Tool
	if b.mcpToolProvider != nil && b.UsesServiceAccountCatalog(bot) {
		// Plugin and unreachable servers drop out of the catalog; their
		// allowlist entries are reported as removed below.
		catalog, _ = b.mcpToolProvider.GetToolsWithSelection(
			ctx,
			mcp.ServiceAccountCatalogRequest(bot.BotUserID(), viewerID),
			mcpToolSelection(cfg, llm.NewContext(), b.isRemoteMCPLicensed()),
		)
	}
	summarizer, _ := b.mcpToolProvider.(ScopedServerSummarizer)

	seen := make(map[string]bool, len(allowlist))
	for _, entry := range allowlist {
		origin := llm.NormalizeMCPServerOrigin(entry.ServerOrigin)
		key := origin + "\x00" + entry.ToolName
		if seen[key] || mcp.ServerKind(origin) == mcp.ServerKindBuiltin {
			continue
		}
		seen[key] = true

		row := ScopedAccessTool{ServerOrigin: origin, ServerKind: mcp.ServerKind(origin), Name: entry.ToolName}
		var summary mcp.ServerSummary
		var known bool
		if summarizer != nil {
			summary, known = summarizer.ServerSummary(origin)
			row.ServerName = summary.Name
		}

		if entry.ToolName == llm.MCPServerToolWildcard {
			row.Reason = ScopedToolRemoval(mcp.ScopedExcludedNotNamed)
			tools = append(tools, row)
			continue
		}

		found := llm.FilterMCPToolsByExplicitAllowlist(catalog, []llm.EnabledMCPTool{entry})
		switch {
		case len(found) > 0:
			row.Reason = ScopedToolRemoval(mcp.ScopedCatalogExclusion(found[0], allowlist))
		case mcp.ScopedOriginExclusion(origin) != mcp.ScopedIncluded:
			row.Reason = ScopedToolRemoval(mcp.ScopedOriginExclusion(origin))
		case row.ServerKind == mcp.ServerKindRemote && known && !summary.ServiceAccountConfigured:
			row.Reason = ScopedRemovedNoServiceAccount
		default:
			row.Reason = ScopedRemovedUnavailable
		}
		row.Offered = row.Reason == ""
		tools = append(tools, row)
	}

	builtIns := b.scopedBuiltInTools(bot, allowlist, seen)
	tools = append(tools, builtIns...)

	slices.SortFunc(tools, func(a, c ScopedAccessTool) int {
		return cmp.Or(
			cmp.Compare(a.ServerKind, c.ServerKind),
			cmp.Compare(a.ServerOrigin, c.ServerOrigin),
			cmp.Compare(a.Name, c.Name),
		)
	})
	return tools, nil
}

// scopedBuiltInTools reports the built-in tools that reach outside Mattermost,
// which a scoped run gets only when the allowlist names them.
func (b *Builder) scopedBuiltInTools(bot *bots.Bot, allowlist []llm.EnabledMCPTool, seen map[string]bool) []ScopedAccessTool {
	var rows []ScopedAccessTool
	var optIn []llm.Tool
	if b.toolProvider != nil {
		for _, tool := range b.toolProvider.GetTools(bot, llm.NewContext()) {
			if tool.ScopedOptIn {
				optIn = append(optIn, tool)
			}
		}
	}
	offered := llm.FilterScopedOptInTools(optIn, allowlist)
	for _, tool := range optIn {
		row := ScopedAccessTool{ServerOrigin: llm.BuiltinToolsOrigin, ServerKind: mcp.ServerKindBuiltin, Name: tool.Name}
		row.Offered = slices.ContainsFunc(offered, func(t llm.Tool) bool { return t.Name == tool.Name })
		if !row.Offered {
			row.Reason = ScopedRemovedNotOptedIn
		}
		rows = append(rows, row)
	}

	for _, entry := range allowlist {
		if llm.NormalizeMCPServerOrigin(entry.ServerOrigin) != llm.BuiltinToolsOrigin {
			continue
		}
		if slices.ContainsFunc(optIn, func(t llm.Tool) bool { return t.Name == entry.ToolName }) {
			continue
		}
		key := llm.BuiltinToolsOrigin + "\x00" + entry.ToolName
		if seen[key] {
			continue
		}
		seen[key] = true
		rows = append(rows, ScopedAccessTool{
			ServerOrigin: llm.BuiltinToolsOrigin,
			ServerKind:   mcp.ServerKindBuiltin,
			Name:         entry.ToolName,
			Reason:       ScopedRemovedUnavailable,
		})
	}
	return rows
}
