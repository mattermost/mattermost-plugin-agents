// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"encoding/json"
	"math/rand"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

func availableTools(req llm.CompletionRequest) []llm.Tool {
	if req.Context == nil || req.Context.Tools == nil {
		return nil
	}
	return req.Context.Tools.GetTools()
}

func chooseWeightedTool(tools []llm.Tool, weights map[string]float64, rng *rand.Rand) (llm.Tool, bool) {
	if len(tools) == 0 || rng == nil {
		return llm.Tool{}, false
	}
	sorted := append([]llm.Tool(nil), tools...)
	slices.SortFunc(sorted, func(a, b llm.Tool) int {
		return strings.Compare(a.Name, b.Name)
	})

	var sum float64
	for _, t := range sorted {
		if w, ok := weights[t.Name]; ok && w > 0 {
			sum += w
		}
	}
	if sum <= 0 {
		return sorted[rng.Intn(len(sorted))], true
	}

	r := rng.Float64() * sum
	for _, t := range sorted {
		w := weights[t.Name]
		if w <= 0 {
			continue
		}
		r -= w
		if r <= 0 {
			return t, true
		}
	}
	return sorted[len(sorted)-1], true
}

func chooseWeightedBuildableTool(profile MockProfile, tools []llm.Tool, weights map[string]float64, ctx *llm.Context, rng *rand.Rand) (llm.Tool, json.RawMessage, bool) {
	eligible := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if canBuildToolArguments(profile, tool, ctx) {
			eligible = append(eligible, tool)
		}
	}
	chosen, ok := chooseWeightedTool(eligible, weights, rng)
	if !ok {
		return llm.Tool{}, nil, false
	}
	args, ok := buildToolArguments(profile, chosen, ctx, rng)
	if !ok {
		return llm.Tool{}, nil, false
	}
	return chosen, args, true
}

func pickInt(rng *rand.Rand, vals []int, fallback []int) int {
	src := vals
	if len(src) == 0 {
		src = fallback
	}
	if len(src) == 0 {
		return 10
	}
	return src[rng.Intn(len(src))]
}

func pickString(rng *rand.Rand, vals []string, fallback string) string {
	if len(vals) == 0 {
		return fallback
	}
	return vals[rng.Intn(len(vals))]
}

func nonEmptyStrings(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func pickValidID(rng *rand.Rand, vals []string) string {
	var valid []string
	for _, v := range vals {
		if model.IsValidId(v) {
			valid = append(valid, v)
		}
	}
	if len(valid) == 0 {
		return ""
	}
	return valid[rng.Intn(len(valid))]
}

func hasValidID(vals []string) bool {
	for _, v := range vals {
		if model.IsValidId(v) {
			return true
		}
	}
	return false
}

func webSearchAllowedURLs(ctx *llm.Context) []string {
	if ctx == nil || ctx.Parameters == nil {
		return nil
	}
	raw, ok := ctx.Parameters["mm_web_search_allowed_urls"]
	if !ok {
		return nil
	}
	urls, ok := raw.([]string)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			out = append(out, u)
		}
	}
	return out
}

func hasRequiredSchema(tool llm.Tool) bool {
	if tool.Schema == nil {
		return false
	}
	switch schema := tool.Schema.(type) {
	case *jsonschema.Schema:
		return schema != nil && len(schema.Required) > 0
	case jsonschema.Schema:
		return len(schema.Required) > 0
	case map[string]any:
		return requiredFieldCount(schema["required"]) > 0
	case json.RawMessage:
		return rawSchemaHasRequired(schema)
	case []byte:
		return rawSchemaHasRequired(schema)
	default:
		raw, err := json.Marshal(schema)
		if err != nil {
			return false
		}
		return rawSchemaHasRequired(raw)
	}
}

func requiredFieldCount(raw any) int {
	switch required := raw.(type) {
	case []string:
		return len(required)
	case []any:
		return len(required)
	default:
		return 0
	}
}

func rawSchemaHasRequired(raw []byte) bool {
	var decoded struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	return len(decoded.Required) > 0
}

func canBuildToolArguments(profile MockProfile, tool llm.Tool, ctx *llm.Context) bool {
	tap := profile.ToolArgumentProfiles[tool.Name]

	switch tool.Name {
	case "read_channel":
		return (ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id)) || hasValidID(tap.ChannelIDs)
	case "read_post":
		return hasValidID(tap.PostIDs)
	case "search_posts", "search_users", "get_user_channels", "WebSearch":
		return true
	case "dm":
		return len(nonEmptyStrings(tap.Usernames)) > 0
	case "WebSearchFetchSource":
		return len(webSearchAllowedURLs(ctx)) > 0
	case "get_channel_info":
		return (ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id)) ||
			hasValidID(tap.ChannelIDs) ||
			len(tap.ChannelNames) > 0
	case "get_channel_members":
		return (ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id)) || hasValidID(tap.ChannelIDs)
	case "create_post":
		return (ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id)) || hasValidID(tap.ChannelIDs)
	case "group_message":
		return len(nonEmptyStrings(tap.Usernames)) >= 2
	default:
		return !hasRequiredSchema(tool)
	}
}

func deterministicMessage(rng *rand.Rand, length int, tag string) string {
	if length <= 0 {
		length = 8
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	var b []byte
	b = append(b, []byte(tag)...)
	for len(b) < length {
		b = append(b, alphabet[rng.Intn(len(alphabet))])
	}
	if len(b) > length {
		b = b[:length]
	}
	return string(b)
}

func splitUsernames(rng *rand.Rand, pool []string, need int) []string {
	if len(pool) < need {
		return nil
	}
	idx := rng.Perm(len(pool))
	out := make([]string, need)
	for i := 0; i < need; i++ {
		out[i] = pool[idx[i]]
	}
	return out
}

// buildToolArguments returns JSON arguments for tool.Name. The second result is false if the tool should be skipped.
func buildToolArguments(profile MockProfile, tool llm.Tool, ctx *llm.Context, rng *rand.Rand) (json.RawMessage, bool) {
	tap := profile.ToolArgumentProfiles[tool.Name]

	switch tool.Name {
	case "read_channel":
		var chID string
		if ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id) {
			chID = ctx.Channel.Id
		} else if len(tap.ChannelIDs) > 0 {
			chID = pickValidID(rng, tap.ChannelIDs)
		}
		if !model.IsValidId(chID) {
			return nil, false
		}
		limit := pickInt(rng, tap.PostLimits, []int{10, 25, 50, 100})
		raw, _ := json.Marshal(map[string]any{
			"channel_id": chID,
			"limit":      limit,
		})
		return raw, true

	case "read_post":
		postID := pickValidID(rng, tap.PostIDs)
		if !model.IsValidId(postID) {
			return nil, false
		}
		inc := rng.Intn(2) == 0
		raw, _ := json.Marshal(map[string]any{
			"post_id":        postID,
			"include_thread": inc,
		})
		return raw, true

	case "search_posts":
		q := pickString(rng, tap.SearchQueries, "loadtest search")
		limitA := pickInt(rng, tap.SearchLimits, []int{10, 25, 50, 100})
		limitB := pickInt(rng, tap.SearchLimits, []int{10, 25, 50})
		arg := map[string]any{
			"query":          q,
			"semantic_limit": limitA,
			"keyword_limit":  limitB,
		}
		if ctx != nil && ctx.Team != nil && model.IsValidId(ctx.Team.Id) {
			arg["team_id"] = ctx.Team.Id
		} else if len(tap.TeamIDs) > 0 {
			arg["team_id"] = pickString(rng, tap.TeamIDs, "")
		}
		if ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id) {
			arg["channel_id"] = ctx.Channel.Id
		} else if len(tap.ChannelIDs) > 0 {
			cid := pickValidID(rng, tap.ChannelIDs)
			if model.IsValidId(cid) {
				arg["channel_id"] = cid
			}
		}
		raw, _ := json.Marshal(arg)
		return raw, true

	case "search_users":
		term := pickString(rng, tap.SearchQueries, "user")
		limit := pickInt(rng, tap.SearchLimits, []int{5, 10, 25})
		raw, _ := json.Marshal(map[string]any{
			"term":  term,
			"limit": limit,
		})
		return raw, true

	case "get_channel_info":
		if ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id) {
			raw, _ := json.Marshal(map[string]any{"channel_id": ctx.Channel.Id})
			return raw, true
		}
		if len(tap.ChannelIDs) > 0 {
			cid := pickValidID(rng, tap.ChannelIDs)
			if model.IsValidId(cid) {
				raw, _ := json.Marshal(map[string]any{"channel_id": cid})
				return raw, true
			}
		}
		if len(tap.ChannelNames) > 0 {
			raw, _ := json.Marshal(map[string]any{"channel_name": pickString(rng, tap.ChannelNames, "town-square")})
			return raw, true
		}
		return nil, false

	case "get_channel_members":
		var chID string
		if ctx != nil && ctx.Channel != nil && model.IsValidId(ctx.Channel.Id) {
			chID = ctx.Channel.Id
		} else if len(tap.ChannelIDs) > 0 {
			chID = pickValidID(rng, tap.ChannelIDs)
		}
		if !model.IsValidId(chID) {
			return nil, false
		}
		limit := pickInt(rng, tap.PostLimits, []int{25, 50, 100})
		page := rng.Intn(3)
		raw, _ := json.Marshal(map[string]any{
			"channel_id": chID,
			"limit":      limit,
			"page":       page,
		})
		return raw, true

	case "get_user_channels":
		arg := map[string]any{
			"per_page": pickInt(rng, tap.PostLimits, []int{30, 60, 100}),
			"page":     rng.Intn(3),
		}
		if ctx != nil && ctx.Team != nil && model.IsValidId(ctx.Team.Id) {
			arg["team_id"] = ctx.Team.Id
		} else if len(tap.TeamIDs) > 0 {
			tid := pickValidID(rng, tap.TeamIDs)
			if model.IsValidId(tid) {
				arg["team_id"] = tid
			}
		}
		raw, _ := json.Marshal(arg)
		return raw, true

	case "create_post":
		var chID, chDisplay, teamDisplay string
		if ctx != nil && ctx.Channel != nil {
			if model.IsValidId(ctx.Channel.Id) {
				chID = ctx.Channel.Id
			}
			chDisplay = ctx.Channel.DisplayName
		}
		if !model.IsValidId(chID) && len(tap.ChannelIDs) > 0 {
			chID = pickValidID(rng, tap.ChannelIDs)
		}
		if ctx != nil && ctx.Team != nil {
			teamDisplay = ctx.Team.DisplayName
		}
		if chDisplay == "" {
			chDisplay = pickString(rng, tap.ChannelNames, "Town Square")
		}
		if teamDisplay == "" {
			teamDisplay = pickString(rng, tap.TeamNames, "Main Team")
		}
		if !model.IsValidId(chID) {
			return nil, false
		}
		if chDisplay == "" || teamDisplay == "" {
			return nil, false
		}
		mlen := pickInt(rng, tap.MessageLengths, []int{20, 200, 3000})
		msg := deterministicMessage(rng, mlen, "cp:")
		raw, _ := json.Marshal(map[string]any{
			"channel_id":           chID,
			"channel_display_name": chDisplay,
			"team_display_name":    teamDisplay,
			"message":              msg,
		})
		return raw, true

	case "dm":
		usernames := nonEmptyStrings(tap.Usernames)
		if len(usernames) == 0 {
			return nil, false
		}
		mlen := pickInt(rng, tap.MessageLengths, []int{12, 100, 2000})
		msg := deterministicMessage(rng, mlen, "dm:")
		arg := map[string]any{
			"username": pickString(rng, usernames, ""),
			"message":  msg,
		}
		raw, _ := json.Marshal(arg)
		return raw, true

	case "group_message":
		pool := nonEmptyStrings(tap.Usernames)
		if len(pool) < 2 {
			return nil, false
		}
		us := splitUsernames(rng, pool, 2)
		if len(us) < 2 {
			return nil, false
		}
		mlen := pickInt(rng, tap.MessageLengths, []int{24, 200, 2800})
		msg := deterministicMessage(rng, mlen, "gm:")
		raw, _ := json.Marshal(map[string]any{
			"usernames": us,
			"message":   msg,
		})
		return raw, true

	case "WebSearch":
		q := pickString(rng, tap.SearchQueries, "mattermost roadmap")
		if len([]rune(q)) < 3 {
			q += " abc"
		}
		raw, _ := json.Marshal(map[string]string{"Query": q})
		return raw, true

	case "WebSearchFetchSource":
		urls := webSearchAllowedURLs(ctx)
		if len(urls) == 0 {
			return nil, false
		}
		u := pickString(rng, urls, "")
		raw, _ := json.Marshal(map[string]string{"URL": u})
		return raw, true

	default:
		if hasRequiredSchema(tool) {
			return nil, false
		}
		raw, _ := json.Marshal(map[string]any{})
		return raw, true
	}
}
