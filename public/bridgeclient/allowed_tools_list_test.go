// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowedToolsList_UnmarshalJSON_strings(t *testing.T) {
	var list AllowedToolsList
	err := json.Unmarshal([]byte(`["search", "create_post"]`), &list)
	require.NoError(t, err)
	require.Equal(t, AllowedToolsList{
		{Name: "search"},
		{Name: "create_post"},
	}, list)
}

func TestAllowedToolsList_UnmarshalJSON_objects(t *testing.T) {
	raw := `[{"server_origin":"https://mcp.example","name":"search"},{"name":"x"}]`
	var list AllowedToolsList
	err := json.Unmarshal([]byte(raw), &list)
	require.NoError(t, err)
	require.Equal(t, AllowedToolsList{
		{ServerOrigin: "https://mcp.example", Name: "search"},
		{Name: "x"},
	}, list)
}

func TestAllowedToolsList_UnmarshalJSON_mixed(t *testing.T) {
	raw := `["legacy_tool", {"server_origin":"","name":"obj_tool"}]`
	var list AllowedToolsList
	err := json.Unmarshal([]byte(raw), &list)
	require.NoError(t, err)
	require.Equal(t, AllowedToolsList{
		{Name: "legacy_tool"},
		{Name: "obj_tool"},
	}, list)
}

func TestAllowedToolsList_MarshalJSON(t *testing.T) {
	list := AllowedToolsList{{Name: "a"}, {ServerOrigin: "o", Name: "b"}}
	b, err := json.Marshal(list)
	require.NoError(t, err)
	require.JSONEq(t, `[{"server_origin":"","name":"a"},{"server_origin":"o","name":"b"}]`, string(b))
}
