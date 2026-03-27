// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateUserPreferencesNormalized(t *testing.T) {
	t.Run("allows empty list", func(t *testing.T) {
		p := &UserToolProviderPreferences{DisabledServers: []string{}}
		require.NoError(t, ValidateUserPreferencesNormalized(p))
	})

	t.Run("rejects too many entries", func(t *testing.T) {
		servers := make([]string, UserPreferencesMaxDisabledServers+1)
		for i := range servers {
			servers[i] = "s"
		}
		p := &UserToolProviderPreferences{DisabledServers: servers}
		err := ValidateUserPreferencesNormalized(p)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUserPreferencesInvalid)
	})

	t.Run("rejects entry too long", func(t *testing.T) {
		long := strings.Repeat("x", UserPreferencesMaxServerEntryLen+1)
		p := &UserToolProviderPreferences{DisabledServers: []string{long}}
		err := ValidateUserPreferencesNormalized(p)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUserPreferencesInvalid)
	})
}
