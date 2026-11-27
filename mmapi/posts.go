// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"cmp"
	"fmt"
	"slices"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/model"
)

// LivePost is the subset of Mattermost's Posts row needed to reconcile
// post-anchored conversation turns against their current live state.
type LivePost struct {
	ID        string `db:"id"`
	ChannelID string `db:"channelid"`
	Message   string `db:"message"`
	EditAt    int64  `db:"editat"`
	DeleteAt  int64  `db:"deleteat"`
}

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
	slices.SortFunc(posts.Order, func(a, b string) int {
		return cmp.Compare(posts.Posts[a].CreateAt, posts.Posts[b].CreateAt)
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

func (db *DBClient) GetFirstPostBeforeTimeRangeID(channelID string, startTime, endTime int64) (string, error) {
	var result struct {
		ID string `db:"id"`
	}
	err := db.DoQuery(&result, db.Builder().
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

// GetPostsByIDs fetches the live state of each post ID in postIDs from
// Mattermost's Posts table in a single primary-key-indexed query. IDs that do
// not exist are absent from the returned map.
func (db *DBClient) GetPostsByIDs(postIDs []string) (map[string]LivePost, error) {
	if len(postIDs) == 0 {
		return map[string]LivePost{}, nil
	}
	var rows []LivePost
	err := db.DoQuery(&rows, db.Builder().
		Select("Id", "ChannelId", "Message", "EditAt", "DeleteAt").
		From("Posts").
		Where(sq.Expr("Id = ANY(?)", pq.Array(postIDs))))
	if err != nil {
		return nil, fmt.Errorf("failed to get posts by ids: %w", err)
	}
	result := make(map[string]LivePost, len(rows))
	for _, row := range rows {
		result[row.ID] = row
	}
	return result, nil
}
