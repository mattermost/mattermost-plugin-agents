// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
)

func TestServiceAccountPolicyWriteNeedsAdmin(t *testing.T) {
	tests := []struct {
		name string
		cfg  *llm.BotConfig
		want bool
	}{
		{name: "nil config does not need admin", cfg: nil},
		{name: "SA off does not need admin", cfg: &llm.BotConfig{UseServiceAccountAuth: false}},
		{name: "SA on needs admin", cfg: &llm.BotConfig{UseServiceAccountAuth: true}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, serviceAccountPolicyWriteNeedsAdmin(tc.cfg))
		})
	}
}
