// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestCheckRegenerateLicense(t *testing.T) {
	threadPost := &model.Post{}
	threadPost.AddProp(ThreadIDProp, "thread-root")
	threadPost.AddProp(AnalysisTypeProp, "summarize_thread")

	channelPost := &model.Post{}
	channelPost.AddProp(AnalysisTypeProp, "summarize_channel")

	recordingPost := &model.Post{}
	recordingPost.AddProp(ReferencedRecordingFileID, "fileid")

	transcriptPost := &model.Post{}
	transcriptPost.AddProp(ReferencedTranscriptPostID, "transcript-post")

	plainPost := &model.Post{}

	tests := []struct {
		name     string
		post     *model.Post
		cap      enterprise.Capability
		minLevel enterprise.Level
		ungated  bool
	}{
		{
			name:     "thread summary",
			post:     threadPost,
			cap:      enterprise.CapThreadSummarization,
			minLevel: enterprise.LevelProfessional,
		},
		{
			name:     "channel summary",
			post:     channelPost,
			cap:      enterprise.CapChannelSummarization,
			minLevel: enterprise.LevelProfessional,
		},
		{
			name:     "recording transcription summary",
			post:     recordingPost,
			cap:      enterprise.CapMeetings,
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name:     "transcription summary",
			post:     transcriptPost,
			cap:      enterprise.CapMeetings,
			minLevel: enterprise.LevelEnterprise,
		},
		{
			name:    "plain conversation post is not gated",
			post:    plainPost,
			ungated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, level := range enterprisetest.AllLevels {
				t.Run(level.String(), func(t *testing.T) {
					c := &Conversations{licenseChecker: enterprisetest.CheckerAt(level)}
					err := c.checkRegenerateLicense(tc.post)
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
				err := c.checkRegenerateLicense(tc.post)
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
