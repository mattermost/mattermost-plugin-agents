// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
)

// TestAccessControlPluginIDMatchesManifest pins the owner segment of the
// plugin's access-control policy types to the manifest. The server enforces
// that a policy type's prefix is the calling plugin's ID, so a plugin rename
// that left accesscontrol.PluginID behind would make every policy read, write,
// and evaluation fail at runtime with nothing to catch it at build time.
func TestAccessControlPluginIDMatchesManifest(t *testing.T) {
	require.Equal(t, manifest.Id, accesscontrol.PluginID)
}
