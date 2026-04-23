// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost-plugin-agents/search"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CombinedSearchArgs represents arguments for search_posts when both semantic and keyword search are available.
type CombinedSearchArgs struct {
	Query          string `json:"query" jsonschema:"The search query,minLength=1,maxLength=4000"`
	TeamID         string `json:"team_id,omitempty" jsonschema:"Optional team ID to limit search scope,minLength=26,maxLength=26"`
	ChannelID      string `json:"channel_id,omitempty" jsonschema:"Optional channel ID to limit search to a specific channel,minLength=26,maxLength=26"`
	SemanticLimit  int    `json:"semantic_limit,omitempty" jsonschema:"Max results from semantic search (default 10; max 50),minimum=1,maximum=50"`
	SemanticOffset int    `json:"semantic_offset,omitempty" jsonschema:"Offset for semantic search pagination,minimum=0"`
	KeywordLimit   int    `json:"keyword_limit,omitempty" jsonschema:"Max results from keyword search (default 10; max 100),minimum=1,maximum=100"`
	KeywordOffset  int    `json:"keyword_offset,omitempty" jsonschema:"Offset for keyword search pagination,minimum=0"`
}

// KeywordOnlySearchArgs represents arguments for search_posts when only keyword search is available.
type KeywordOnlySearchArgs struct {
	Query         string `json:"query" jsonschema:"The search query,minLength=1,maxLength=4000"`
	TeamID        string `json:"team_id,omitempty" jsonschema:"Optional team ID to limit search scope,minLength=26,maxLength=26"`
	ChannelID     string `json:"channel_id,omitempty" jsonschema:"Optional channel ID to limit search to a specific channel,minLength=26,maxLength=26"`
	KeywordLimit  int    `json:"keyword_limit,omitempty" jsonschema:"Max results from keyword search (default 10; max 100),minimum=1,maximum=100"`
	KeywordOffset int    `json:"keyword_offset,omitempty" jsonschema:"Offset for keyword search pagination,minimum=0"`
}

// SearchUsersArgs represents arguments for the search_users tool.
type SearchUsersArgs struct {
	Term  string `json:"term" jsonschema:"Search term (username, email, first name, or last name),minLength=1,maxLength=64"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default: 20, max: 100),minimum=1,maximum=100"`
}

// searchPostsRegistration returns schema and description for search_posts (shared with tests).
func (p *MattermostToolProvider) searchPostsRegistration() (*jsonschema.Schema, string) {
	semanticEnabled := p.searchService != nil && p.searchService.Enabled()

	var schema *jsonschema.Schema
	var description string

	contextHint := "Results show individual matching posts — to see the full conversation around a result, use read_channel with the channel_id."

	if semanticEnabled {
		schema = llm.NewJSONSchemaFromStruct[CombinedSearchArgs]()
		description = "Search for posts in Mattermost using both semantic (AI-powered) and keyword search. " +
			"Semantic search finds posts by meaning and does not require exact term matches. " +
			"Keyword search uses AND logic — all terms must appear in a single post, so prefer short, focused queries (1-2 key terms) over long multi-word phrases. " +
			"Parameters: query (required), team_id (optional), channel_id (optional). " +
			"semantic_limit/semantic_offset control semantic results (default: 10). " +
			"keyword_limit/keyword_offset control keyword results (default: 10). " +
			"You can make separate calls with different queries optimized for each search type (e.g., a natural language query for semantic and specific keywords for keyword search). " +
			"Returns matching posts with content, author, channel, and relevance score for semantic results. " +
			contextHint
	} else {
		schema = llm.NewJSONSchemaFromStruct[KeywordOnlySearchArgs]()
		description = "Search for posts in Mattermost using keyword search. " +
			"Uses AND logic — all terms must appear in a single post, so prefer short, focused queries (1-2 key terms) over long multi-word phrases. " +
			"Parameters: query (required), team_id (optional), channel_id (optional). " +
			"keyword_limit/keyword_offset control results (default: 10). " +
			"Returns matching posts with content, author, and channel. " +
			contextHint
	}
	return schema, description
}

// provideSearchTools registers all search-related MCP tools.
func (p *MattermostToolProvider) provideSearchTools(s *mcp.Server) {
	schema, description := p.searchPostsRegistration()
	registerTool[CombinedSearchArgs](s, p, "search_posts", description, schema, p.toolCombinedSearch, format.SearchPostsOutput)
	registerTool[SearchUsersArgs](s, p, "search_users",
		"Search for existing users by username, email, or name. Parameters: term (required search text), limit (1-100, default 20). Returns user details including username, email, display name, and position for matching users. Example: {\"term\": \"john\", \"limit\": 5}",
		llm.NewJSONSchemaFromStruct[SearchUsersArgs](),
		p.toolSearchUsers,
		format.SearchUsersOutput,
	)
}

// buildSearchTermWithChannel prepends an in:channelname modifier to the search query.
func buildSearchTermWithChannel(query, channelName string) string {
	return "in:" + channelName + " " + query
}

func buildSearchPostsOutput(query string, semanticResults, keywordResults []mcptool.SearchPostResult, semanticEnabled bool, channelIDFilter string) mcptool.SearchPostsOutput {
	seenPostIDs := make(map[string]bool)
	for _, r := range semanticResults {
		seenPostIDs[r.Post.Id] = true
	}
	dedupedKeyword := make([]mcptool.SearchPostResult, 0, len(keywordResults))
	for _, r := range keywordResults {
		if !seenPostIDs[r.Post.Id] {
			dedupedKeyword = append(dedupedKeyword, r)
		}
	}
	return mcptool.SearchPostsOutput{
		Query:           query,
		ChannelIDFilter: channelIDFilter,
		SemanticEnabled: semanticEnabled,
		SemanticResults: semanticResults,
		KeywordResults:  dedupedKeyword,
		Terms:           strings.Fields(query),
	}
}

// toolCombinedSearch implements the search_posts tool.
func (p *MattermostToolProvider) toolCombinedSearch(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.SearchPostsOutput, error) {
	var args CombinedSearchArgs
	if err := argsGetter(&args); err != nil {
		return mcptool.SearchPostsOutput{}, fmt.Errorf("failed to get arguments for tool search_posts: %w", err)
	}

	if args.Query == "" {
		return mcptool.SearchPostsOutput{}, fmt.Errorf("query cannot be empty")
	}

	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return mcptool.SearchPostsOutput{}, fmt.Errorf("team_id must be a valid ID")
	}
	if args.ChannelID != "" && !model.IsValidId(args.ChannelID) {
		return mcptool.SearchPostsOutput{}, fmt.Errorf("channel_id must be a valid ID")
	}

	if args.SemanticLimit <= 0 {
		args.SemanticLimit = 10
	}
	if args.SemanticLimit > 50 {
		args.SemanticLimit = 50
	}
	if args.SemanticOffset < 0 {
		args.SemanticOffset = 0
	}
	if args.KeywordLimit <= 0 {
		args.KeywordLimit = 10
	}
	if args.KeywordLimit > 100 {
		args.KeywordLimit = 100
	}
	if args.KeywordOffset < 0 {
		args.KeywordOffset = 0
	}

	if mcpContext.Client == nil {
		return mcptool.SearchPostsOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	semanticEnabled := p.searchService != nil && p.searchService.Enabled()
	userID := ""
	if semanticEnabled {
		user, _, err := client.GetMe(ctx, "")
		if err != nil {
			return mcptool.SearchPostsOutput{}, fmt.Errorf("failed to get current user: %w", err)
		}
		userID = user.Id
	}

	var semanticResults []mcptool.SearchPostResult
	var keywordResults []mcptool.SearchPostResult
	var semanticErr, keywordErr error
	var wg sync.WaitGroup

	if semanticEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			semanticResults, semanticErr = p.executeSemanticSearch(ctx, client, args, userID)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		keywordResults, keywordErr = p.executeKeywordSearch(ctx, client, args)
	}()

	wg.Wait()

	if semanticErr != nil {
		p.logger.Warn("semantic search failed", "error", semanticErr)
	}
	if keywordErr != nil {
		p.logger.Warn("keyword search failed", "error", keywordErr)
	}

	if keywordErr != nil && (!semanticEnabled || semanticErr != nil) {
		if semanticEnabled {
			return mcptool.SearchPostsOutput{}, fmt.Errorf("both search methods failed: semantic: %v, keyword: %v", semanticErr, keywordErr)
		}
		return mcptool.SearchPostsOutput{}, fmt.Errorf("keyword search failed: %v", keywordErr)
	}

	output := buildSearchPostsOutput(args.Query, semanticResults, keywordResults, semanticEnabled, args.ChannelID)
	return output, nil
}

// executeSemanticSearch runs the semantic search and returns enriched results.
func (p *MattermostToolProvider) executeSemanticSearch(ctx context.Context, client *model.Client4, args CombinedSearchArgs, userID string) ([]mcptool.SearchPostResult, error) {
	opts := search.Options{
		Limit:     args.SemanticLimit,
		Offset:    args.SemanticOffset,
		TeamID:    args.TeamID,
		ChannelID: args.ChannelID,
		UserID:    userID,
	}

	results, err := p.searchService.Search(ctx, args.Query, opts)
	if err != nil {
		return nil, err
	}

	channelTeamCache := make(map[string]string)
	for _, r := range results {
		if _, exists := channelTeamCache[r.ChannelID]; exists {
			continue
		}

		channel, _, chErr := client.GetChannel(ctx, r.ChannelID)
		if chErr == nil && channel.TeamId != "" {
			team, _, teamErr := client.GetTeam(ctx, channel.TeamId, "")
			if teamErr == nil {
				channelTeamCache[r.ChannelID] = team.DisplayName
				continue
			}
		}

		channelTeamCache[r.ChannelID] = ""
	}

	postResults := make([]mcptool.SearchPostResult, 0, len(results))
	for _, r := range results {
		postResults = append(postResults, mcptool.SearchPostResult{
			Post: &model.Post{
				Id:        r.PostID,
				ChannelId: r.ChannelID,
				UserId:    r.UserID,
				Message:   r.Content,
			},
			ChannelName: r.ChannelName,
			TeamName:    channelTeamCache[r.ChannelID],
			Username:    r.Username,
			Score:       r.Score,
			Source:      "semantic",
		})
	}

	return postResults, nil
}

// executeKeywordSearch runs the Mattermost keyword search and returns enriched results.
func (p *MattermostToolProvider) executeKeywordSearch(ctx context.Context, client *model.Client4, args CombinedSearchArgs) ([]mcptool.SearchPostResult, error) {
	searchTerm := args.Query
	teamID := args.TeamID

	channelCache := make(map[string]*model.Channel)
	teamCache := make(map[string]*model.Team)
	userCache := make(map[string]*model.User)

	if args.ChannelID != "" {
		channel, _, chErr := client.GetChannel(ctx, args.ChannelID)
		if chErr != nil {
			return nil, fmt.Errorf("error fetching channel %s: %w", args.ChannelID, chErr)
		}

		searchTerm = buildSearchTermWithChannel(searchTerm, channel.Name)
		channelCache[args.ChannelID] = channel
		if teamID != "" && teamID != channel.TeamId {
			return nil, fmt.Errorf("channel %s belongs to team %s, not %s", args.ChannelID, channel.TeamId, teamID)
		}
		teamID = channel.TeamId
	}

	searchResults, _, err := client.SearchPosts(ctx, teamID, searchTerm, false)
	if err != nil {
		return nil, err
	}

	posts := make([]*model.Post, 0, len(searchResults.Posts))
	for _, post := range searchResults.Posts {
		if args.ChannelID != "" && post.ChannelId != args.ChannelID {
			continue
		}
		posts = append(posts, post)
	}

	if len(posts) == 0 {
		return nil, nil
	}

	sort.Slice(posts, func(i, j int) bool {
		if posts[i].CreateAt != posts[j].CreateAt {
			return posts[i].CreateAt > posts[j].CreateAt
		}
		return posts[i].Id > posts[j].Id
	})

	if args.KeywordOffset > 0 && args.KeywordOffset < len(posts) {
		posts = posts[args.KeywordOffset:]
	} else if args.KeywordOffset >= len(posts) {
		return nil, nil
	}

	if len(posts) > args.KeywordLimit {
		posts = posts[:args.KeywordLimit]
	}

	for _, post := range posts {
		if _, exists := channelCache[post.ChannelId]; !exists {
			channel, _, chErr := client.GetChannel(ctx, post.ChannelId)
			if chErr == nil {
				channelCache[post.ChannelId] = channel
			} else {
				channelCache[post.ChannelId] = nil
			}
		}

		if channel := channelCache[post.ChannelId]; channel != nil && channel.TeamId != "" {
			if _, exists := teamCache[channel.TeamId]; !exists {
				team, _, teamErr := client.GetTeam(ctx, channel.TeamId, "")
				if teamErr == nil {
					teamCache[channel.TeamId] = team
				} else {
					teamCache[channel.TeamId] = nil
				}
			}
		}

		if _, exists := userCache[post.UserId]; !exists {
			user, _, userErr := client.GetUser(ctx, post.UserId, "")
			if userErr == nil {
				userCache[post.UserId] = user
			} else {
				p.logger.Warn("failed to get user for post", "user_id", post.UserId, "error", userErr)
				userCache[post.UserId] = nil
			}
		}
	}

	postResults := make([]mcptool.SearchPostResult, 0, len(posts))
	for _, post := range posts {
		result := mcptool.SearchPostResult{
			Post:   post,
			Source: "keyword",
		}

		if channel := channelCache[post.ChannelId]; channel != nil {
			switch channel.Type {
			case model.ChannelTypeDirect:
				result.ChannelName = "Direct Message"
			case model.ChannelTypeGroup:
				result.ChannelName = "Group Message"
			default:
				result.ChannelName = channel.DisplayName
			}

			if team := teamCache[channel.TeamId]; team != nil {
				result.TeamName = team.DisplayName
			}
		}

		if user := userCache[post.UserId]; user != nil {
			result.Username = user.Username
		}

		postResults = append(postResults, result)
	}

	return postResults, nil
}

// toolSearchUsers implements the search_users tool.
func (p *MattermostToolProvider) toolSearchUsers(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.SearchUsersOutput, error) {
	var args SearchUsersArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.SearchUsersOutput{}, fmt.Errorf("failed to get arguments for tool search_users: %w", err)
	}

	if args.Term == "" {
		return mcptool.SearchUsersOutput{}, fmt.Errorf("search term cannot be empty")
	}

	if args.Limit <= 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	if mcpContext.Client == nil {
		return mcptool.SearchUsersOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	searchOptions := &model.UserSearch{
		Term:          args.Term,
		Limit:         args.Limit,
		AllowInactive: false,
		WithoutTeam:   false,
	}

	users, _, err := client.SearchUsers(ctx, searchOptions)
	if err != nil {
		return mcptool.SearchUsersOutput{}, fmt.Errorf("error searching users: %w", err)
	}

	out := mcptool.SearchUsersOutput{
		Term:  args.Term,
		Users: users,
	}
	return out, nil
}
