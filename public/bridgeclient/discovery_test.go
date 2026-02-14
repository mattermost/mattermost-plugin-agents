// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAgentToolsValidation(t *testing.T) {
	client := &Client{}

	_, err := client.GetAgentTools("bad", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent ID")

	validID := "abcdefghijklmnopqrstuvwxyz"
	_, err = client.GetAgentTools(validID, "bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user ID")
}
