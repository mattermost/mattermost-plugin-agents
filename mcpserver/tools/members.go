// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// renderMember is the subset of a channel/team membership needed to render a
// member listing: the user id and the member's formatted role.
type renderMember struct {
	userID string
	role   string
}

// renderMembers resolves each member's user details and formats them as a paged
// listing. noun labels the listing (e.g. "Channel Members"). Bot accounts are
// dropped when excludeBots is set, and the count is reported in the footer.
func (p *MattermostToolProvider) renderMembers(ctx context.Context, client *model.Client4, noun string, page int, members []renderMember, excludeBots bool) string {
	var result strings.Builder
	botsExcluded := 0
	written := 0

	for _, m := range members {
		user, _, err := client.GetUser(ctx, m.userID, "")
		if err != nil {
			p.logger.Warn("failed to get user details for member", "user_id", m.userID, "error", err)
			format.WriteUser(&result, format.UserEntry{User: &model.User{Id: m.userID, Username: "details unavailable"}})
			written++
			continue
		}

		if excludeBots && user.IsBot {
			botsExcluded++
			continue
		}

		format.WriteUser(&result, format.UserEntry{User: user, Role: m.role})
		written++
	}

	header := fmt.Sprintf("%s (page %d, showing %d members):\n", noun, page, written)
	footer := ""
	if botsExcluded > 0 {
		footer = fmt.Sprintf("\n(%d bot account(s) excluded — set exclude_bots=false to include them)\n", botsExcluded)
	}
	return header + result.String() + footer
}
