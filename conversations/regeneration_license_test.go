// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

func TestCheckRegenerateLicense(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		cap       enterprise.Capability
		minLevel  enterprise.Level
		ungated   bool
	}{
		{
			name:      "thread summary",
			operation: llm.OperationThreadAnalysis,
			cap:       enterprise.CapThreadSummarization,
			minLevel:  enterprise.LevelProfessional,
		},
		{
			name:      "channel summary",
			operation: llm.OperationChannelSummary,
			cap:       enterprise.CapChannelSummarization,
			minLevel:  enterprise.LevelProfessional,
		},
		{
			name:      "channel interval summary",
			operation: llm.OperationChannelInterval,
			cap:       enterprise.CapChannelSummarization,
			minLevel:  enterprise.LevelProfessional,
		},
		{
			name:      "meeting summary",
			operation: llm.OperationMeetingSummary,
			cap:       enterprise.CapMeetings,
			minLevel:  enterprise.LevelEnterprise,
		},
		{
			name:      "plain conversation post is not gated",
			operation: llm.OperationConversation,
			ungated:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, level := range enterprisetest.AllLevels {
				t.Run(level.String(), func(t *testing.T) {
					c := &Conversations{licenseChecker: enterprisetest.CheckerAt(level)}
					err := c.checkRegenerateLicense(tc.operation)
					if tc.ungated || level >= tc.minLevel {
						require.NoError(t, err)
						return
					}
					var licErr *enterprise.LicenseError
					require.Error(t, err)
					require.True(t, errors.As(err, &licErr))
					require.Equal(t, tc.cap, licErr.Capability)
					require.Equal(t, tc.minLevel, licErr.RequiredLevel)
				})
			}

			t.Run("nil checker fails closed", func(t *testing.T) {
				c := &Conversations{}
				err := c.checkRegenerateLicense(tc.operation)
				if tc.ungated {
					require.NoError(t, err)
					return
				}
				var licErr *enterprise.LicenseError
				require.Error(t, err)
				require.True(t, errors.As(err, &licErr))
			})
		})
	}
}
