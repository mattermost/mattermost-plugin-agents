// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	svg "github.com/h2non/go-is-svg"
	"github.com/stretchr/testify/require"
)

// The server only loads plugin icons that pass go-is-svg's check.
func TestPluginIconIsSVG(t *testing.T) {
	manifestJSON, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		IconPath string `json:"icon_path"`
	}
	require.NoError(t, json.Unmarshal(manifestJSON, &manifest))
	require.NotEmpty(t, manifest.IconPath)

	icon, err := os.ReadFile(filepath.Join("..", manifest.IconPath))
	require.NoError(t, err)
	require.True(t, svg.Is(icon), "%s is not an SVG the server accepts", manifest.IconPath)
}
