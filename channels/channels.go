// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channels

import (
	"slices"

	"github.com/mattermost/mattermost-plugin-ai/format"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost/server/public/model"
)

type Channels struct {
	llm      llm.LanguageModel
	prompts  *llm.Prompts
	client   mmapi.Client
	dbClient *mmapi.DBClient
}

func New(
	llm llm.LanguageModel,
	prompts *llm.Prompts,
	client mmapi.Client,
	dbClient *mmapi.DBClient,
) *Channels {
	return &Channels{
		llm:      llm,
		prompts:  prompts,
		client:   client,
		dbClient: dbClient,
	}
}

func (c *Channels) Interval(
	context *llm.Context,
	channelID string,
	startTime int64,
	endTime int64,
	promptName string,
) (*llm.TextStreamResult, error) {
	var posts *model.PostList
	var err error
	if endTime == 0 {
		posts, err = c.client.GetPostsSince(channelID, startTime)
	} else {
		posts, err = c.getPostsByChannelBetween(channelID, startTime, endTime)
	}
	if err != nil {
		return nil, err
	}

	threadData, err := mmapi.GetMetadataForPosts(c.client, posts)
	if err != nil {
		return nil, err
	}

	// Remove deleted posts
	threadData.Posts = slices.DeleteFunc(threadData.Posts, func(post *model.Post) bool {
		return post.DeleteAt != 0
	})

	formattedThread := format.ThreadData(threadData)

	context.Parameters = map[string]any{
		"Thread": formattedThread,
	}
	systemPrompt, err := c.prompts.Format(promptName, context)
	if err != nil {
		return nil, err
	}

	userPrompt, err := c.prompts.Format(prompts.PromptThreadUser, context)
	if err != nil {
		return nil, err
	}

	completionRequest := llm.CompletionRequest{
		Posts: []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: systemPrompt,
			},
			{
				Role:    llm.PostRoleUser,
				Message: userPrompt,
			},
		},
		Context: context,
	}

	resultStream, err := c.llm.ChatCompletion(completionRequest, llm.WithToolsDisabled())
	if err != nil {
		return nil, err
	}

	return resultStream, nil
}

const (
	postsPerPage = 60
	maxPosts     = 200
)

func (c *Channels) getPostsByChannelBetween(channelID string, startTime, endTime int64) (*model.PostList, error) {
	// Find the ID of first post in our time range
	firstPostID, err := c.dbClient.GetFirstPostBeforeTimeRangeID(channelID, startTime, endTime)
	if err != nil {
		return nil, err
	}

	// Initialize result list
	result := &model.PostList{
		Posts: make(map[string]*model.Post),
		Order: []string{},
	}

	// Keep fetching previous pages until we either:
	// 1. Reach the endTime
	// 2. Hit the maxPosts limit
	// 3. Run out of posts
	totalPosts := 0
	page := 0

	for totalPosts < maxPosts {
		morePosts, err := c.client.GetPostsBefore(channelID, firstPostID, page, postsPerPage)
		if err != nil {
			return nil, err
		}

		if len(morePosts.Posts) == 0 {
			break // No more posts
		}

		// Add posts that fall within our time range
		for _, post := range morePosts.Posts {
			if post.CreateAt >= startTime && post.CreateAt <= endTime {
				result.Posts[post.Id] = post
				result.Order = append([]string{post.Id}, result.Order...)
				totalPosts++
				if totalPosts >= maxPosts {
					break
				}
			}
			if post.CreateAt < startTime {
				break // We've gone too far back
			}
		}

		page++
	}

	return result, nil
}
