// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceChannelsFromMeta(t *testing.T) {
	public := &model.Channel{Id: "ch-public", Type: model.ChannelTypeOpen, TeamId: "team-a"}
	private := &model.Channel{Id: "ch-private", Type: model.ChannelTypePrivate, TeamId: "team-a"}
	wirePublic := map[string]any{"id": public.Id, "type": string(public.Type), "team_id": public.TeamId}
	wirePrivate := map[string]any{"id": private.Id, "type": string(private.Type), "team_id": private.TeamId}

	cases := []struct {
		name    string
		meta    sdkmcp.Meta
		wantOK  bool
		wantIDs []string
	}{
		{name: "nil meta", meta: nil, wantOK: false},
		{name: "missing key", meta: sdkmcp.Meta{"other": "x"}, wantOK: false},
		{
			name:    "native channel pointers",
			meta:    sdkmcp.Meta{sourceChannelsParam: []*model.Channel{public, private}},
			wantOK:  true,
			wantIDs: []string{public.Id, private.Id},
		},
		{
			name:    "empty native slice marks provenance",
			meta:    sdkmcp.Meta{sourceChannelsParam: []*model.Channel{}},
			wantOK:  true,
			wantIDs: []string{},
		},
		{
			name:    "any-slice of maps (tool-result wire shape)",
			meta:    sdkmcp.Meta{sourceChannelsParam: []any{wirePublic, wirePrivate}},
			wantOK:  true,
			wantIDs: []string{public.Id, private.Id},
		},
		{
			name:    "any-slice of native pointers",
			meta:    sdkmcp.Meta{sourceChannelsParam: []any{public, private}},
			wantOK:  true,
			wantIDs: []string{public.Id, private.Id},
		},
		{
			name:    "typed map slice",
			meta:    sdkmcp.Meta{sourceChannelsParam: []map[string]any{wirePublic, wirePrivate}},
			wantOK:  true,
			wantIDs: []string{public.Id, private.Id},
		},
		{
			name:    "empty any-slice marks provenance",
			meta:    sdkmcp.Meta{sourceChannelsParam: []any{}},
			wantOK:  true,
			wantIDs: []string{},
		},
		{
			name:   "unexpected value type",
			meta:   sdkmcp.Meta{sourceChannelsParam: "not-a-list"},
			wantOK: false,
		},
		{
			name:   "map missing id",
			meta:   sdkmcp.Meta{sourceChannelsParam: []any{map[string]any{"type": "O", "team_id": "team-a"}}},
			wantOK: false,
		},
		{
			name:   "map with empty id",
			meta:   sdkmcp.Meta{sourceChannelsParam: []any{map[string]any{"id": "", "type": "O"}}},
			wantOK: false,
		},
		{
			name:   "non-string id",
			meta:   sdkmcp.Meta{sourceChannelsParam: []any{map[string]any{"id": 123, "type": "O"}}},
			wantOK: false,
		},
		{
			name:   "nil pointer in any-slice",
			meta:   sdkmcp.Meta{sourceChannelsParam: []any{(*model.Channel)(nil)}},
			wantOK: false,
		},
		{
			name:   "unsupported item type in any-slice",
			meta:   sdkmcp.Meta{sourceChannelsParam: []any{"ch-public"}},
			wantOK: false,
		},
		{
			name:   "one bad item fails the whole list",
			meta:   sdkmcp.Meta{sourceChannelsParam: []any{wirePublic, map[string]any{"type": "P"}}},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sourceChannelsFromMeta(tc.meta)
			require.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				assert.Nil(t, got)
				return
			}
			require.Len(t, got, len(tc.wantIDs))
			for i, id := range tc.wantIDs {
				assert.Equal(t, id, got[i].Id)
			}
		})
	}
}

func TestSourceChannelsFromMetaJSONRoundTrip(t *testing.T) {
	// Embedded tools encode sources as []any of maps; MCP transport JSON-encodes Meta.
	wire := sdkmcp.Meta{sourceChannelsParam: []any{
		map[string]any{"id": "ch-1", "type": string(model.ChannelTypeOpen), "team_id": "team-a"},
		map[string]any{"id": "ch-2", "type": string(model.ChannelTypePrivate), "team_id": "team-a"},
	}}
	encoded, err := json.Marshal(wire)
	require.NoError(t, err)

	var decoded sdkmcp.Meta
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	got, ok := sourceChannelsFromMeta(decoded)
	require.True(t, ok)
	require.Len(t, got, 2)
	assert.Equal(t, "ch-1", got[0].Id)
	assert.Equal(t, model.ChannelTypeOpen, got[0].Type)
	assert.Equal(t, "team-a", got[0].TeamId)
	assert.Equal(t, "ch-2", got[1].Id)
	assert.Equal(t, model.ChannelTypePrivate, got[1].Type)
}

func TestChannelFromMetaMapOptionalFields(t *testing.T) {
	ch, ok := channelFromMetaMap(map[string]any{"id": "ch-only"})
	require.True(t, ok)
	assert.Equal(t, "ch-only", ch.Id)
	assert.Equal(t, model.ChannelType(""), ch.Type)
	assert.Equal(t, "", ch.TeamId)

	ch, ok = channelFromMetaMap(map[string]any{
		"id":      "ch-full",
		"type":    1,
		"team_id": 2,
	})
	require.True(t, ok)
	assert.Equal(t, "ch-full", ch.Id)
	assert.Equal(t, model.ChannelType(""), ch.Type)
	assert.Equal(t, "", ch.TeamId)
}

func TestMergeSourceChannels(t *testing.T) {
	a := &model.Channel{Id: "a", Type: model.ChannelTypeOpen, TeamId: "team-a"}
	aAgain := &model.Channel{Id: "a", Type: model.ChannelTypePrivate, TeamId: "team-b"}
	b := &model.Channel{Id: "b", Type: model.ChannelTypePrivate, TeamId: "team-a"}
	empty := &model.Channel{Id: "", Type: model.ChannelTypePrivate}

	cases := []struct {
		name     string
		existing []*model.Channel
		added    []*model.Channel
		wantIDs  []string
	}{
		{
			name:     "nil inputs",
			existing: nil,
			added:    nil,
			wantIDs:  []string{},
		},
		{
			name:     "appends new ids",
			existing: []*model.Channel{a},
			added:    []*model.Channel{b},
			wantIDs:  []string{"a", "b"},
		},
		{
			name:     "duplicate id keeps the first entry",
			existing: []*model.Channel{a},
			added:    []*model.Channel{aAgain},
			wantIDs:  []string{"a"},
		},
		{
			name:     "skips nil entries",
			existing: []*model.Channel{a, nil},
			added:    []*model.Channel{nil, b},
			wantIDs:  []string{"a", "b"},
		},
		{
			name:     "empty ids are kept and not used as a dedup key",
			existing: []*model.Channel{empty},
			added:    []*model.Channel{empty, b},
			wantIDs:  []string{"", "", "b"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeSourceChannels(tc.existing, tc.added)
			require.Len(t, got, len(tc.wantIDs))
			for i, id := range tc.wantIDs {
				assert.Equal(t, id, got[i].Id)
			}
			if len(tc.wantIDs) == 1 && tc.wantIDs[0] == "a" {
				assert.Equal(t, model.ChannelTypeOpen, got[0].Type)
				assert.Equal(t, "team-a", got[0].TeamId)
			}
		})
	}
}

func TestApplySourceChannelsMeta(t *testing.T) {
	t.Run("nil context is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() {
			applySourceChannelsMeta(nil, "read_channel", sdkmcp.Meta{sourceChannelsParam: []any{
				map[string]any{"id": "ch-1", "type": "O", "team_id": "team-a"},
			}})
		})
	})

	t.Run("unparseable meta leaves parameters untouched", func(t *testing.T) {
		ctx := &llm.Context{Parameters: map[string]any{"keep": true}}
		applySourceChannelsMeta(ctx, "read_channel", sdkmcp.Meta{sourceChannelsParam: "bad"})
		assert.Equal(t, true, ctx.Parameters["keep"])
		_, present := ctx.Parameters[sourceChannelsParam]
		assert.False(t, present)
	})

	t.Run("missing key leaves parameters untouched", func(t *testing.T) {
		ctx := &llm.Context{}
		applySourceChannelsMeta(ctx, "read_channel", sdkmcp.Meta{"other": 1})
		assert.Nil(t, ctx.Parameters)
	})

	t.Run("creates parameters and stores parsed channels", func(t *testing.T) {
		ctx := &llm.Context{}
		applySourceChannelsMeta(ctx, "read_channel", sdkmcp.Meta{sourceChannelsParam: []any{
			map[string]any{"id": "ch-1", "type": string(model.ChannelTypeOpen), "team_id": "team-a"},
		}})
		got, ok := ctx.Parameters[sourceChannelsParam].([]*model.Channel)
		require.True(t, ok)
		require.Len(t, got, 1)
		assert.Equal(t, "ch-1", got[0].Id)
		assert.Equal(t, model.ChannelTypeOpen, got[0].Type)
		assert.Equal(t, "team-a", got[0].TeamId)
	})

	t.Run("merges later tool results and skips a subsequent parse failure", func(t *testing.T) {
		ctx := &llm.Context{}
		applySourceChannelsMeta(ctx, "read_channel", sdkmcp.Meta{sourceChannelsParam: []any{
			map[string]any{"id": "ch-1", "type": string(model.ChannelTypeOpen), "team_id": "team-a"},
		}})
		applySourceChannelsMeta(ctx, "read_channel", sdkmcp.Meta{sourceChannelsParam: []any{
			map[string]any{"id": "ch-2", "type": string(model.ChannelTypePrivate), "team_id": "team-a"},
		}})
		applySourceChannelsMeta(ctx, "read_channel", sdkmcp.Meta{sourceChannelsParam: "bad"})

		got, ok := ctx.Parameters[sourceChannelsParam].([]*model.Channel)
		require.True(t, ok)
		require.Len(t, got, 2)
		assert.Equal(t, "ch-1", got[0].Id)
		assert.Equal(t, "ch-2", got[1].Id)
	})

	t.Run("empty list still records an empty source list", func(t *testing.T) {
		ctx := &llm.Context{}
		applySourceChannelsMeta(ctx, "read_channel", sdkmcp.Meta{sourceChannelsParam: []any{}})
		got, ok := ctx.Parameters[sourceChannelsParam].([]*model.Channel)
		require.True(t, ok)
		assert.Empty(t, got)
	})
}
