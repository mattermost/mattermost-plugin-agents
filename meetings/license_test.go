// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/subtitles"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestMeetingsServiceLicenseGate(t *testing.T) {
	methods := []struct {
		name string
		call func(s *Service) error
	}{
		{
			name: "HandleTranscribeFile",
			call: func(s *Service) error {
				_, err := s.HandleTranscribeFile("user", nil, nil, nil, "file")
				return err
			},
		},
		{
			name: "HandleSummarizeTranscription",
			call: func(s *Service) error {
				_, err := s.HandleSummarizeTranscription("user", nil, nil, nil)
				return err
			},
		},
		{
			name: "HandlePostbackSummary",
			call: func(s *Service) error {
				_, err := s.HandlePostbackSummary("user", &model.Post{})
				return err
			},
		},
		{
			name: "summarizeCallRecording",
			call: func(s *Service) error {
				return s.summarizeCallRecording(nil, "root", &model.User{}, "file", nil)
			},
		},
		{
			name: "SummarizeTranscription",
			call: func(s *Service) error {
				_, err := s.SummarizeTranscription(t.Context(), nil, &subtitles.Subtitles{}, nil)
				return err
			},
		},
	}

	for _, method := range methods {
		t.Run(method.name, func(t *testing.T) {
			for _, level := range enterprisetest.AllLevels {
				t.Run(level.String(), func(t *testing.T) {
					s := &Service{licenseChecker: enterprisetest.CheckerAt(level)}
					if level >= enterprise.LevelEnterprise {
						require.NoError(t, s.checkMeetingsLicense())
						return
					}
					err := method.call(s)
					var licErr *enterprise.LicenseError
					require.Error(t, err)
					require.True(t, errors.As(err, &licErr))
					require.Equal(t, enterprise.CapMeetings, licErr.Capability)
					require.Equal(t, enterprise.LevelEnterprise, licErr.RequiredLevel)
				})
			}

			t.Run("nil checker fails closed", func(t *testing.T) {
				s := &Service{}
				err := method.call(s)
				var licErr *enterprise.LicenseError
				require.Error(t, err)
				require.True(t, errors.As(err, &licErr))
			})

			t.Run("nil service fails closed", func(t *testing.T) {
				var s *Service
				err := method.call(s)
				var licErr *enterprise.LicenseError
				require.Error(t, err)
				require.True(t, errors.As(err, &licErr))
			})
		})
	}
}
