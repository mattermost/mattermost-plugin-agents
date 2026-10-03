// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/stretchr/testify/require"
)

func TestSearchServiceLicenseGate(t *testing.T) {
	me := mocks.NewMockEmbeddingSearch(t)
	getSearch := func() embeddings.EmbeddingSearch { return me }

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			s := New(getSearch, nil, nil, nil, enterprisetest.CheckerAt(level), nil)
			licensed := level >= enterprise.LevelEnterprise
			require.Equal(t, licensed, s.Enabled())

			if licensed {
				return
			}

			_, err := s.Search(t.Context(), "hello", Options{Limit: 5})
			var licErr *enterprise.LicenseError
			require.Error(t, err)
			require.True(t, errors.As(err, &licErr))
			require.Equal(t, enterprise.CapSemanticSearch, licErr.Capability)

			_, runErr := s.RunSearch(t.Context(), "user", nil, "hello", "", "", 5)
			require.Error(t, runErr)
			require.True(t, errors.As(runErr, &licErr))
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		s := New(getSearch, nil, nil, nil, nil, nil)
		require.False(t, s.Enabled())
		_, err := s.Search(t.Context(), "hello", Options{Limit: 5})
		var licErr *enterprise.LicenseError
		require.Error(t, err)
		require.True(t, errors.As(err, &licErr))

		_, runErr := s.RunSearch(t.Context(), "user", nil, "hello", "", "", 5)
		require.Error(t, runErr)
		require.True(t, errors.As(runErr, &licErr))
	})
}

func TestInitEmbeddingsSearchLicense(t *testing.T) {
	cfg := embeddings.EmbeddingSearchConfig{
		Type:       embeddings.SearchTypeComposite,
		Dimensions: 1536,
	}

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			search, err := InitEmbeddingsSearch(nil, cfg, enterprisetest.CheckerAt(level), false)
			if level < enterprise.LevelEnterprise {
				require.Error(t, err)
				require.Contains(t, err.Error(), "available at Enterprise and above")
				require.Nil(t, search)
				return
			}
			if err != nil {
				require.NotContains(t, err.Error(), "available at Enterprise and above")
			}
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		search, err := InitEmbeddingsSearch(nil, cfg, nil, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "available at Enterprise and above")
		require.Nil(t, search)
	})
}
