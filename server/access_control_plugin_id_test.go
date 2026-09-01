// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
)

// Pins the policy-type owner segment to plugin.json's id. The server requires
// that prefix; a rename that left PluginID behind would fail every policy
// call at runtime with nothing to catch it at build time.
func TestAccessControlPluginIDMatchesManifest(t *testing.T) {
	require.Equal(t, manifest.Id, accesscontrol.PluginID)
}
