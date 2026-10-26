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

func TestDirectMessageSystemPromptGenerativeUI(t *testing.T) {
	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	tests := []struct {
		name         string
		generativeUI bool
	}{
		{name: "disabled", generativeUI: false},
		{name: "enabled", generativeUI: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := llm.NewContext()
			ctx.BotName = "Matty"
			ctx.GenerativeUI = tt.generativeUI

			prompt, err := promptsEngine.Format(prompts.PromptDirectMessageQuestionSystem, ctx)
			require.NoError(t, err)

			if !tt.generativeUI {
				assert.NotContains(t, prompt, "openui-lang")
				return
			}

			assert.Contains(t, prompt, "```openui-lang")
			// The component reference generated from the webapp library is included.
			assert.Contains(t, prompt, "Stack(children:")
			assert.Contains(t, prompt, "Table(columns: string[]")
			assert.NotContains(t, prompt, "{{")
		})
	}
}
