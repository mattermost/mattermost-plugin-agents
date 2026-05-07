// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"context"
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func makeStore(names ...string) *llm.ToolStore {
	s := llm.NewToolStore(nil, false)
	var tools []llm.Tool
	for _, n := range names {
		tools = append(tools, llm.Tool{Name: n, ServerOrigin: "origin-" + n})
	}
	s.AddTools(tools)
	return s
}

func TestChooseWeightedIgnoresUnavailableTools(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))
	tools := []llm.Tool{{Name: "read_channel"}, {Name: "search_posts"}}
	weights := map[string]float64{"read_channel": 1.0, "missing": 50.0}
	ch, ok := chooseWeightedTool(tools, weights, rng)
	require.True(t, ok)
	require.Equal(t, "read_channel", ch.Name)
}

func TestChooseWeightedUniformFallback(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7))
	tools := []llm.Tool{{Name: "a"}, {Name: "b"}}
	ch, ok := chooseWeightedTool(tools, map[string]float64{}, rng)
	require.True(t, ok)
	require.Contains(t, []string{"a", "b"}, ch.Name)
}

func TestReadChannelArgsChannelID(t *testing.T) {
	t.Parallel()
	cid := model.NewId()
	profile := DefaultReadSearchHeavyProfile()
	ctx := &llm.Context{
		Channel: &model.Channel{Id: cid},
		Tools:   makeStore("read_channel"),
	}
	tool := llm.Tool{Name: "read_channel"}
	rng := rand.New(rand.NewSource(1))
	raw, ok := buildToolArguments(profile, tool, ctx, rng)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Equal(t, cid, m["channel_id"])
	limit := int(m["limit"].(float64))
	require.Contains(t, []int{10, 25, 50, 100}, limit)
}

func TestSearchPostsValidJSON(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	ctx := &llm.Context{
		Team:    &model.Team{Id: model.NewId()},
		Channel: &model.Channel{Id: model.NewId()},
	}
	tool := llm.Tool{Name: "search_posts"}
	rng := rand.New(rand.NewSource(99))
	raw, ok := buildToolArguments(profile, tool, ctx, rng)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.NotEmpty(t, m["query"])
}

func TestCreatePostMessageLengths(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	chID := model.NewId()
	ctx := &llm.Context{
		Channel: &model.Channel{Id: chID, DisplayName: "Town"},
		Team:    &model.Team{Id: model.NewId(), DisplayName: "Team Co"},
	}
	tool := llm.Tool{Name: "create_post"}
	rng := rand.New(rand.NewSource(1000))
	raw, ok := buildToolArguments(profile, tool, ctx, rng)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	msg := m["message"].(string)
	require.GreaterOrEqual(t, len(msg), 12)
}

func TestDMAndGroupMessageLengths(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	ctx := &llm.Context{}
	rng := rand.New(rand.NewSource(2))

	dmTool := llm.Tool{Name: "dm"}
	dmRaw, ok := buildToolArguments(profile, dmTool, ctx, rng)
	require.True(t, ok)
	var dm map[string]any
	require.NoError(t, json.Unmarshal(dmRaw, &dm))
	require.NotEmpty(t, dm["message"])
	require.NotEmpty(t, dm["username"])

	gmTool := llm.Tool{Name: "group_message"}
	gmRaw, ok := buildToolArguments(profile, gmTool, ctx, rng)
	require.True(t, ok)
	var gm map[string]any
	require.NoError(t, json.Unmarshal(gmRaw, &gm))
	us := gm["usernames"].([]any)
	require.Len(t, us, 2)
}

func TestDMSkipsWithoutRecipient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		usernames []string
	}{
		{
			name: "missing",
		},
		{
			name:      "empty",
			usernames: []string{""},
		},
		{
			name:      "whitespace only",
			usernames: []string{" \t\n "},
		},
		{
			name:      "empty and whitespace only",
			usernames: []string{"", " \t "},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profile := DefaultReadSearchHeavyProfile()
			profile.ToolArgumentProfiles["dm"] = ToolArgumentProfile{
				MessageLengths: []int{20},
				Usernames:      tt.usernames,
			}
			tool := llm.Tool{Name: "dm"}
			require.False(t, canBuildToolArguments(profile, tool, nil))

			raw, ok := buildToolArguments(profile, tool, nil, rand.New(rand.NewSource(4)))
			require.False(t, ok)
			require.Nil(t, raw)
		})
	}
}

func TestSkipCreatePostWithoutChannelContext(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	ctx := &llm.Context{Channel: &model.Channel{DisplayName: "x"}} // no id
	tool := llm.Tool{Name: "create_post"}
	rng := rand.New(rand.NewSource(3))
	_, ok := buildToolArguments(profile, tool, ctx, rng)
	require.False(t, ok)
}

func TestChooseWeightedBuildableToolSkipsUnbuildableTools(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	delete(profile.ToolArgumentProfiles, "group_message")
	ctx := &llm.Context{Channel: &model.Channel{Id: model.NewId()}}
	tools := []llm.Tool{
		{Name: "group_message"},
		{Name: "read_channel"},
	}
	weights := map[string]float64{
		"group_message": 1000,
		"read_channel":  1,
	}
	tool, args, ok := chooseWeightedBuildableTool(profile, tools, weights, ctx, rand.New(rand.NewSource(11)))
	require.True(t, ok)
	require.Equal(t, "read_channel", tool.Name)
	var readArgs map[string]any
	require.NoError(t, json.Unmarshal(args, &readArgs))
	require.Equal(t, ctx.Channel.Id, readArgs["channel_id"])
}

func TestChooseWeightedBuildableToolHonorsEligibleWeights(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	delete(profile.ToolArgumentProfiles, "group_message")
	ctx := &llm.Context{Channel: &model.Channel{Id: model.NewId()}}
	tools := []llm.Tool{
		{Name: "group_message"},
		{Name: "read_channel"},
		{Name: "search_posts"},
	}
	weights := map[string]float64{
		"group_message": 1000,
		"read_channel":  0,
		"search_posts":  1,
	}
	for seed := int64(0); seed < 20; seed++ {
		tool, args, ok := chooseWeightedBuildableTool(profile, tools, weights, ctx, rand.New(rand.NewSource(seed)))
		require.True(t, ok)
		require.Equal(t, "search_posts", tool.Name)
		var searchArgs map[string]any
		require.NoError(t, json.Unmarshal(args, &searchArgs))
		require.NotEmpty(t, searchArgs["query"])
		require.Equal(t, ctx.Channel.Id, searchArgs["channel_id"])
	}
}

func TestUnknownToolSchemaRequiredControlsEligibility(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	tests := []struct {
		name string
		tool llm.Tool
		ok   bool
	}{
		{
			name: "schema-less unknown is eligible",
			tool: llm.Tool{Name: "schema_less_unknown"},
			ok:   true,
		},
		{
			name: "optional unknown is eligible",
			tool: llm.Tool{
				Name:   "optional_unknown",
				Schema: &jsonschema.Schema{},
			},
			ok: true,
		},
		{
			name: "required unknown is skipped",
			tool: llm.Tool{
				Name: "required_unknown",
				Schema: &jsonschema.Schema{
					Required: []string{"query"},
				},
			},
			ok: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, ok := buildToolArguments(profile, tt.tool, nil, rand.New(rand.NewSource(12)))
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.JSONEq(t, `{}`, string(raw))
			} else {
				require.Nil(t, raw)
			}
		})
	}
}

func TestWebSearchFetchSourceUsesAllowedContextURL(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	ctx := &llm.Context{
		Parameters: map[string]interface{}{
			"mm_web_search_allowed_urls": []string{
				"https://mattermost.com/blog/page",
				"https://docs.mattermost.com/agents",
			},
		},
	}

	raw, ok := buildToolArguments(profile, llm.Tool{Name: "WebSearchFetchSource"}, ctx, rand.New(rand.NewSource(1)))
	require.True(t, ok)

	var args map[string]string
	require.NoError(t, json.Unmarshal(raw, &args))
	require.Contains(t, ctx.Parameters["mm_web_search_allowed_urls"], args["URL"])
	require.NotContains(t, args["URL"], "example.com")
}

func TestWebSearchFetchSourceSkipsWithoutAllowedURL(t *testing.T) {
	t.Parallel()
	profile := DefaultReadSearchHeavyProfile()
	raw, ok := buildToolArguments(profile, llm.Tool{Name: "WebSearchFetchSource"}, &llm.Context{}, rand.New(rand.NewSource(1)))
	require.False(t, ok)
	require.Nil(t, raw)
}

func TestServerOriginPreservedInStream(t *testing.T) {
	t.Parallel()
	require.NoError(t, DefaultReadSearchHeavyProfile().Validate())
	store := llm.NewToolStore(nil, false)
	store.AddTools([]llm.Tool{
		{Name: "read_channel", ServerOrigin: "https://mcp.example"},
	})
	ctx := &llm.Context{
		Channel: &model.Channel{Id: model.NewId()},
		Tools:   store,
	}
	p := fastTestProfile()
	p.ToolUseProbability = 1.0
	p.MaxToolRounds = 10
	p.ReasoningSkipProbability = 1.0

	m := NewMockLLM(p)
	req := llm.CompletionRequest{Context: ctx}
	res, err := m.ChatCompletion(context.Background(), req)
	require.NoError(t, err)
	for ev := range res.Stream {
		if ev.Type == llm.EventTypeToolCalls {
			tcs := ev.Value.([]llm.ToolCall)
			require.Len(t, tcs, 1)
			require.Equal(t, "https://mcp.example", tcs[0].ServerOrigin)
			return
		}
	}
	require.Fail(t, "expected tool calls")
}

// fastTestProfile returns a valid profile with instant timings for tests.
func fastTestProfile() MockProfile {
	p := DefaultReadSearchHeavyProfile()
	for k := range p.LatencyProfiles {
		p.LatencyProfiles[k] = LatencyProfile{
			TTFTMs:                    [2]int{0, 0},
			ChunkCount:                [2]int{1, 2},
			ChunkIntervalMs:           [2]int{0, 0},
			TotalWallTimeMsPerRequest: [2]int{0, 0},
		}
	}
	p.StreamingEnabled = true
	return p
}
