package bridgeclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMattermostAccessScope_EffectiveAccessibleChannelTypes(t *testing.T) {
	t.Run("prefers canonical field", func(t *testing.T) {
		scope := &MattermostAccessScope{
			AccessibleChannelTypes: []string{"O", "P"},
		}

		types, err := scope.EffectiveAccessibleChannelTypes()
		require.NoError(t, err)
		require.Equal(t, []string{"O", "P"}, types)
	})

	t.Run("accepts deprecated alias", func(t *testing.T) {
		scope := &MattermostAccessScope{
			AllowedChannelTypes: []string{"D"},
		}

		types, err := scope.EffectiveAccessibleChannelTypes()
		require.NoError(t, err)
		require.Equal(t, []string{"D"}, types)
	})

	t.Run("rejects conflicting fields", func(t *testing.T) {
		scope := &MattermostAccessScope{
			AccessibleChannelTypes: []string{"O"},
			AllowedChannelTypes:    []string{"P"},
		}

		types, err := scope.EffectiveAccessibleChannelTypes()
		require.Error(t, err)
		require.Nil(t, types)
		require.Contains(t, err.Error(), "accessible_channel_types")
		require.Contains(t, err.Error(), "allowed_channel_types")
	})
}

func TestNewClientUsesPluginTransport(t *testing.T) {
	client := NewClient(&fakePluginAPI{})
	require.NotNil(t, client)

	transport, ok := client.httpClient.Transport.(*pluginAPIRoundTripper)
	require.True(t, ok)
	require.NotNil(t, transport.api)
}

func TestNewClientFromAppUsesAppTransportAndUserID(t *testing.T) {
	client := NewClientFromApp(&fakeAppAPI{}, "abcdefghijklmnopqrstuvwxyz")
	require.NotNil(t, client)

	transport, ok := client.httpClient.Transport.(*appAPIRoundTripper)
	require.True(t, ok)
	require.NotNil(t, transport.api)
	require.Equal(t, "abcdefghijklmnopqrstuvwxyz", transport.userID)
}
