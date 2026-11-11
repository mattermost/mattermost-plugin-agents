// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package prompts_test

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerativeUIPromptOnlyInSkill(t *testing.T) {
	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	ctx := llm.NewContext()
	ctx.BotName = "Matty"

	skill, err := promptsEngine.Format(prompts.PromptSkillGenerativeUI, ctx)
	require.NoError(t, err)
	assert.Contains(t, skill, "```openui-lang")
	// The component reference generated from the webapp library is included.
	assert.Contains(t, skill, "Stack(children:")
	assert.Contains(t, skill, "Table(columns: string[]")
	assert.NotContains(t, skill, "{{")

	system, err := promptsEngine.Format(prompts.PromptDirectMessageQuestionSystem, ctx)
	require.NoError(t, err)
	assert.NotContains(t, system, "openui-lang")
}
