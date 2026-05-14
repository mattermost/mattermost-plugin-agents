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
	"github.com/mattermost/mattermost-plugin-agents/search"
	"github.com/mattermost/mattermost/server/public/model"
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

// getSearchTools returns all search-related tools.
func (p *MattermostToolProvider) getSearchTools() []MCPTool {
	semanticEnabled := p.searchService != nil && p.searchService.Enabled()

	var schema *jsonschema.Schema
	var description string

	contextHint := "Results show individual matching posts — to see the full conversation around a result, use read_channel with the channel_id."

	mentionHint := "To find posts mentioning a specific person, query their username only (e.g. `john.smith`); at-mentions in posts use the username, never the display name. Do not combine username and display name (e.g. `john.smith John Smith`) in a single query — that requires all of those tokens to co-occur and will miss most posts."

	if semanticEnabled {
		schema = llm.NewJSONSchemaFromStruct[CombinedSearchArgs]()
		description = "Search for posts in Mattermost using both semantic (AI-powered) and keyword search. " +
			"Semantic search finds posts by meaning and does not require exact term matches. " +
			"Keyword search treats the query as a literal AND match — every whitespace-separated token must appear in the same post — so prefer short, focused queries (1-2 key terms) over long multi-word phrases. " +
			"Parameters: query (required), team_id (optional), channel_id (optional). " +
			"semantic_limit/semantic_offset control semantic results (default: 10). " +
			"keyword_limit/keyword_offset control keyword results (default: 10). " +
			"You can make separate calls with different queries optimized for each search type (e.g., a natural language query for semantic and specific keywords for keyword search). " +
			mentionHint + " " +
			"Returns matching posts with content, author, channel, and relevance score for semantic results. " +
			contextHint
	} else {
		schema = llm.NewJSONSchemaFromStruct[KeywordOnlySearchArgs]()
		description = "Search for posts in Mattermost using keyword search. " +
			"Treats the query as a literal AND match — every whitespace-separated token must appear in the same post — so prefer short, focused queries (1-2 key terms) over long multi-word phrases. " +
			"Parameters: query (required), team_id (optional), channel_id (optional). " +
			"keyword_limit/keyword_offset control results (default: 10). " +
			mentionHint + " " +
			"Returns matching posts with content, author, and channel. " +
			contextHint
	}

	return []MCPTool{
		{
			Name:        "search_posts",
			Description: description,
			Schema:      schema,
			Resolver:    p.toolCombinedSearch,
		},
		{
			Name:        "search_users",
			Description: "Search for existing users by username, email, or name. Parameters: term (required search text), limit (1-100, default 20). Returns user details including username, email, display name, and position for matching users. Example: {\"term\": \"john\", \"limit\": 5}",
			Schema:      llm.NewJSONSchemaFromStruct[SearchUsersArgs](),
			Resolver:    p.toolSearchUsers,
		},
	}
}

// buildSearchTermWithChannel prepends an in:channelname modifier to the search query.
func buildSearchTermWithChannel(query, channelName string) string {
	return "in:" + channelName + " " + query
}

// searchPostResult holds a post result with metadata for deduplication and formatting.
type searchPostResult struct {
	Post        *model.Post
	ChannelName string
	TeamName    string
	Username    string
	Score       float32 // Only set for semantic results.
	Source      string  // "semantic" or "keyword".
}

// toolCombinedSearch implements the search_posts tool.
func (p *MattermostToolProvider) toolCombinedSearch(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CombinedSearchArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool search_posts: %w", err)
	}

	if args.Query == "" {
		return "query is required", fmt.Errorf("query cannot be empty")
	}

	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
	}
	if args.ChannelID != "" && !model.IsValidId(args.ChannelID) {
		return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
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
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	semanticEnabled := p.searchService != nil && p.searchService.Enabled()
	userID := ""
	if semanticEnabled {
		user, _, err := client.GetMe(ctx, "")
		if err != nil {
			return "failed to get user", fmt.Errorf("failed to get current user: %w", err)
		}
		userID = user.Id
	}

	var semanticResults []searchPostResult
	var keywordResults []searchPostResult
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
			return "search failed", fmt.Errorf("both search methods failed: semantic: %v, keyword: %v", semanticErr, keywordErr)
		}
		return "search failed", fmt.Errorf("keyword search failed: %v", keywordErr)
	}

	return p.formatCombinedResults(args.Query, semanticResults, keywordResults, semanticEnabled, args.ChannelID)
}

// executeSemanticSearch runs the semantic search and returns enriched results.
func (p *MattermostToolProvider) executeSemanticSearch(ctx context.Context, client *model.Client4, args CombinedSearchArgs, userID string) ([]searchPostResult, error) {
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

	postResults := make([]searchPostResult, 0, len(results))
	for _, r := range results {
		postResults = append(postResults, searchPostResult{
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
func (p *MattermostToolProvider) executeKeywordSearch(ctx context.Context, client *model.Client4, args CombinedSearchArgs) ([]searchPostResult, error) {
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

	postResults := make([]searchPostResult, 0, len(posts))
	for _, post := range posts {
		result := searchPostResult{
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

// formatCombinedResults formats the combined search results into a readable string.
func (p *MattermostToolProvider) formatCombinedResults(query string, semanticResults, keywordResults []searchPostResult, semanticEnabled bool, channelIDFilter string) (string, error) {
	// Deduplicate: if a post appears in both, keep it in semantic only.
	seenPostIDs := make(map[string]bool)
	for _, r := range semanticResults {
		seenPostIDs[r.Post.Id] = true
	}

	dedupedKeywordResults := make([]searchPostResult, 0, len(keywordResults))
	for _, r := range keywordResults {
		if !seenPostIDs[r.Post.Id] {
			dedupedKeywordResults = append(dedupedKeywordResults, r)
		}
	}

	totalSemantic := len(semanticResults)
	totalKeyword := len(dedupedKeywordResults)
	total := totalSemantic + totalKeyword

	if total == 0 {
		terms := strings.Fields(query)
		if len(terms) > 2 {
			return fmt.Sprintf("No posts found for %q (%d terms). All terms must appear in a single post — try fewer terms (1-2).", query, len(terms)), nil
		}
		return fmt.Sprintf("No posts found for %q.", query), nil
	}

	var result strings.Builder

	noun := "results"
	if total == 1 {
		noun = "result"
	}
	if semanticEnabled {
		result.WriteString(fmt.Sprintf("Found %d %s for \"%s\" (%d semantic, %d keyword):\n", total, noun, query, totalSemantic, totalKeyword))
	} else {
		result.WriteString(fmt.Sprintf("Found %d %s for \"%s\":\n", total, noun, query))
	}

	if channelIDFilter != "" {
		result.WriteString(fmt.Sprintf("Channel ID filter: %s\n", channelIDFilter))
	}

	if semanticEnabled && totalSemantic > 0 {
		result.WriteString("\n## Semantic Search Results\n\n")
		for i, r := range semanticResults {
			p.formatSingleResult(&result, i+1, r, true, channelIDFilter)
		}
	}

	if totalKeyword > 0 {
		if semanticEnabled {
			result.WriteString("\n## Keyword Search Results\n\n")
		} else {
			result.WriteString("\n")
		}
		for i, r := range dedupedKeywordResults {
			p.formatSingleResult(&result, i+1, r, false, channelIDFilter)
		}
	}

	return result.String(), nil
}

// formatSingleResult formats a single search result.
func (p *MattermostToolProvider) formatSingleResult(result *strings.Builder, index int, r searchPostResult, includeScore bool, channelIDFilter string) {
	var score float32
	if includeScore {
		score = r.Score
	}
	username := r.Username
	if username != "" {
		username = "@" + username
	}
	format.WritePost(result, format.PostEntry{
		HeaderLabel: fmt.Sprintf("Result %d", index),
		Username:    username,
		Score:       score,
		Post:        r.Post,
		ChannelName: r.ChannelName,
		TeamName:    r.TeamName,
		ShowChannel: channelIDFilter == "",
	})
}

// toolSearchUsers implements the search_users tool.
func (p *MattermostToolProvider) toolSearchUsers(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args SearchUsersArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool search_users: %w", err)
	}

	if args.Term == "" {
		return "term is required", fmt.Errorf("search term cannot be empty")
	}

	if args.Limit <= 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
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
		return "user search failed", fmt.Errorf("error searching users: %w", err)
	}

	if len(users) == 0 {
		return "no users found matching the search criteria", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d users matching '%s':\n\n", len(users), args.Term))

	for i, user := range users {
		format.WriteUser(&result, format.UserEntry{
			HeaderLabel: fmt.Sprintf("User %d", i+1),
			User:        user,
		})
	}

	return result.String(), nil
}
