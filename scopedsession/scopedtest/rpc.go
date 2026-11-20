// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package scopedtest has test helpers for code that reads server settings
// through the plugin API.
package scopedtest

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// RPCConfig returns cfg as a plugin receives it from GetConfig: the plugin RPC
// encodes values with gob, which drops pointers to zero values, so a false
// *bool setting arrives as nil.
func RPCConfig(t *testing.T, cfg *model.Config) *model.Config {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(cfg))
	decoded := &model.Config{}
	require.NoError(t, gob.NewDecoder(&buf).Decode(decoded))
	return decoded
}
