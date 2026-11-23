// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost/server/public/model"
)

// maxUsernameBaseLength keeps the project agent's username readable in
// mentions, well under Mattermost's username limit.
const maxUsernameBaseLength = 40

// randomUsernameAttempts is how many random suffixes are tried once every
// numbered username is taken.
const randomUsernameAttempts = 3

// agentUsernameBase is the slug a project agent's username starts from: the
// channel's URL name, or its display name when the URL name is generated.
func agentUsernameBase(channel *model.Channel) string {
	base := ""
	if !model.IsValidId(channel.Name) {
		base = slug(channel.Name)
	}
	if base == "" {
		base = slug(channel.DisplayName)
	}
	if base == "" {
		base = "project"
	}
	if base[0] >= '0' && base[0] <= '9' {
		base = "p-" + base
	}
	if len(base) > maxUsernameBaseLength {
		base = base[:maxUsernameBaseLength]
		if cut := strings.LastIndexByte(base, '-'); cut > maxUsernameBaseLength/2 {
			base = base[:cut]
		}
		base = strings.TrimRight(base, "-_")
	}
	return base
}

// slug lowercases s and keeps letters, digits, hyphens and underscores,
// turning anything else into single hyphens.
func slug(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			hyphen = false
		case !hyphen && b.Len() > 0:
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.Trim(b.String(), "-_")
}

// agentUsernameCandidates lists the usernames a project agent may get, best
// first: <base>-agent, then numbered, then with random suffixes.
func agentUsernameCandidates(channel *model.Channel) []string {
	name := agentUsernameBase(channel) + "-agent"
	candidates := []string{name}
	for i := 2; i <= 9; i++ {
		candidates = append(candidates, name+"-"+strconv.Itoa(i))
	}
	for range randomUsernameAttempts {
		candidates = append(candidates, name+"-"+strings.ToLower(model.NewId()[:4]))
	}
	return candidates
}

// validAgentUsername reports whether name is a username both Mattermost and
// the agents store accept.
func validAgentUsername(name string) bool {
	return model.IsValidUsername(name) && agentconfig.ValidUsername(name)
}

// agentDisplayName is the name people see for the agent of channel.
func agentDisplayName(channel *model.Channel) string {
	name := strings.TrimSpace(channel.DisplayName)
	if name == "" {
		name = "Project"
	}
	suffix := " Agent"
	if limit := model.BotDisplayNameMaxRunes - utf8.RuneCountInString(suffix); utf8.RuneCountInString(name) > limit {
		name = strings.TrimSpace(string([]rune(name)[:limit]))
	}
	return name + suffix
}
