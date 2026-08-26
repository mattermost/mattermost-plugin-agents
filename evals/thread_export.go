// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// ThreadExport represents the format of exported thread data
type ThreadExport struct {
	Posts     map[string]*model.Post     `json:"posts"`
	Channel   *model.Channel             `json:"channel"`
	Team      *model.Team                `json:"team"`
	Users     map[string]*model.User     `json:"users"`
	FileInfos map[string]*model.FileInfo `json:"file_infos"`
	Files     map[string][]byte          `json:"files"`

	// Helper fields not in the JSON
	RootPost *model.Post     `json:"-"`
	PostList *model.PostList `json:"-"`
}

func (t *ThreadExport) RequestingUser() *model.User {
	return t.Users[t.LatestPost().UserId]
}

func (t *ThreadExport) LatestPost() *model.Post {
	return t.PostList.Posts[t.PostList.Order[0]]
}

func (t *ThreadExport) String() string {
	var result strings.Builder

	// Header with team/channel info
	result.WriteString(fmt.Sprintf("Thread Export: %s > %s\n", t.Team.DisplayName, t.Channel.DisplayName))
	result.WriteString(fmt.Sprintf("Posts: %d\n\n", len(t.PostList.Order)))

	// Posts in reverse chronological order (root post first)
	for _, postID := range slices.Backward(t.PostList.Order) {

		post := t.PostList.Posts[postID]
		user := t.Users[post.UserId]

		// Post header
		if post.RootId == "" {
			result.WriteString(fmt.Sprintf("[ROOT] %s (@%s) - %s\n",
				user.GetDisplayName(model.ShowFullName), user.Username,
				time.Unix(post.CreateAt/1000, 0).Format("2006-01-02 15:04:05")))
		} else {
			result.WriteString(fmt.Sprintf("[REPLY] %s (@%s) - %s\n",
				user.GetDisplayName(model.ShowFullName), user.Username,
				time.Unix(post.CreateAt/1000, 0).Format("2006-01-02 15:04:05")))
		}

		// Post content
		if post.Message != "" {
			result.WriteString(fmt.Sprintf("  %s\n", post.Message))
		}

		// File attachments
		if len(post.FileIds) > 0 {
			result.WriteString("  Attachments:\n")
			for _, fileID := range post.FileIds {
				if fileInfo, exists := t.FileInfos[fileID]; exists {
					result.WriteString(fmt.Sprintf("    - %s (%s)\n", fileInfo.Name, fileInfo.MimeType))
				} else {
					result.WriteString(fmt.Sprintf("    - File ID: %s (info not available)\n", fileID))
				}
			}
		}

		result.WriteString("\n")
	}

	return result.String()
}

// LoadThreadFromJSON loads post data from a JSON file containing exported Mattermost thread data
// and returns it as ThreadData containing Posts, RootPost, and PostList for testing
func LoadThreadFromJSON(t *EvalT, path string) *ThreadExport {
	// Open the JSON file
	jsonFile, err := os.ReadFile(path)
	require.NoError(t, err, "Failed to read test data file: %s", path)

	// Parse the JSON data
	var threadExport ThreadExport
	err = json.Unmarshal(jsonFile, &threadExport)
	require.NoError(t, err, "Failed to unmarshal JSON data")
	require.NotEmpty(t, threadExport.Posts, "No posts loaded from file")

	// Convert thread map to slice of posts
	posts := make([]*model.Post, 0, len(threadExport.Posts))
	for _, post := range threadExport.Posts {
		posts = append(posts, post)
	}

	// Find the root post (the one with empty root_id)
	var rootPost *model.Post
	for _, post := range posts {
		if post.RootId == "" {
			rootPost = post
			break
		}
	}
	require.NotNil(t, rootPost, "Root post not found in thread data")

	// Create a model.PostList from the posts
	postList := &model.PostList{
		Order: make([]string, len(posts)),
		Posts: make(map[string]*model.Post),
	}

	// Add all posts to the postList
	for i, post := range posts {
		postList.Order[i] = post.Id
		postList.Posts[post.Id] = post
	}

	postList.SortByCreateAt()

	threadExport.RootPost = rootPost
	threadExport.PostList = postList

	return &threadExport
}
