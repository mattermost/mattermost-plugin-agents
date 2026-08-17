// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package prompts_test

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedFrenchMeetingSummaryPrompt(t *testing.T) {
	engine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	en, err := engine.Format(prompts.PromptMeetingSummarySystem, llm.NewContext())
	require.NoError(t, err)
	require.Contains(t, en, "key discussion points")

	fr, err := engine.Format(prompts.PromptMeetingSummarySystem, llm.NewContext(func(c *llm.Context) {
		c.RequestingUser = &model.User{Locale: "fr_FR"}
	}))
	require.NoError(t, err)
	require.Contains(t, fr, "## Résumé")
	require.Contains(t, fr, "## Points de discussion")
	require.Contains(t, fr, "## Actions")
	require.False(t, strings.Contains(fr, "key discussion points"))
}
