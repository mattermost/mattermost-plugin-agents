// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"fmt"
	"slices"
	"sort"

	sq "github.com/Masterminds/squirrel"
	"github.com/mattermost/mattermost/server/public/model"
)

type ThreadData struct {
	Posts     []*model.Post
	UsersByID map[string]*model.User
}

func (t *ThreadData) CutoffBeforePostID(postID string) {
	// Iterate in reverse because it's more likely that the post we are responding to is near the end.
	for i, post := range slices.Backward(t.Posts) {
		if post.Id == postID {
			t.Posts = t.Posts[:i]
			break
		}
	}
}

func GetThreadData(client Client, postID string) (*ThreadData, error) {
	posts, err := client.GetPostThread(postID)
	if err != nil {
		return nil, err
	}
	return GetMetadataForPosts(client, posts)
}

func GetMetadataForPosts(client Client, posts *model.PostList) (*ThreadData, error) {
	sort.Slice(posts.Order, func(i, j int) bool {
		return posts.Posts[posts.Order[i]].CreateAt < posts.Posts[posts.Order[j]].CreateAt
	})

	userIDsUnique := make(map[string]bool)
	for _, post := range posts.Posts {
		userIDsUnique[post.UserId] = true
	}
	userIDs := make([]string, 0, len(userIDsUnique))
	for userID := range userIDsUnique {
		userIDs = append(userIDs, userID)
	}

	usersByID := make(map[string]*model.User)
	for _, userID := range userIDs {
		user, err := client.GetUser(userID)
		if err != nil {
			return nil, err
		}
		usersByID[userID] = user
	}

	postsSlice := posts.ToSlice()

	return &ThreadData{
		Posts:     postsSlice,
		UsersByID: usersByID,
	}, nil
}

func (c *DBClient) GetFirstPostBeforeTimeRangeID(channelID string, startTime, endTime int64) (string, error) {
	var result struct {
		ID string `db:"id"`
	}
	err := c.DoQuery(&result, c.Builder().
		Select("id").
		From("Posts").
		Where(sq.Eq{"ChannelId": channelID}).
		Where(sq.And{
			sq.GtOrEq{"CreateAt": startTime},
			sq.LtOrEq{"CreateAt": endTime},
			sq.Eq{"DeleteAt": 0},
		}).
		OrderBy("CreateAt ASC").
		Limit(1))

	if err != nil {
		return "", fmt.Errorf("failed to get first post ID: %w", err)
	}

	return result.ID, nil
}
