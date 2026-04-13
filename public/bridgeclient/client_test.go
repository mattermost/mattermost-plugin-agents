package bridgeclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
