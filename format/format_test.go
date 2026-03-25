// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestThreadData(t *testing.T) {
	testCases := []struct {
		name     string
		data     *mmapi.ThreadData
		expected string
	}{
		{
			name: "single post thread",
			data: &mmapi.ThreadData{
				Posts: []*model.Post{
					{
						UserId:  "user1",
						Message: "Hello world",
					},
				},
				UsersByID: map[string]*model.User{
					"user1": {
						Username: "johndoe",
					},
				},
			},
			expected: "johndoe: Hello world\n\n",
		},
		{
			name: "multiple posts thread",
			data: &mmapi.ThreadData{
				Posts: []*model.Post{
					{
						UserId:  "user1",
						Message: "Hello",
					},
					{
						UserId:  "user2",
						Message: "Hi there",
					},
					{
						UserId:  "user1",
						Message: "How are you?",
					},
				},
				UsersByID: map[string]*model.User{
					"user1": {
						Username: "johndoe",
					},
					"user2": {
						Username: "janedoe",
					},
				},
			},
			expected: "johndoe: Hello\n\njanedoe: Hi there\n\njohndoe: How are you?\n\n",
		},
		{
			name: "thread with attachments",
			data: &mmapi.ThreadData{
				Posts: []*model.Post{
					{
						UserId:  "user1",
						Message: "Post with attachment",
						Props: map[string]any{
							"attachments": []any{
								map[string]any{
									"text": "Attachment content",
								},
							},
						},
					},
				},
				UsersByID: map[string]*model.User{
					"user1": {
						Username: "johndoe",
					},
				},
			},
			expected: "johndoe: Post with attachment\nAttachment content\n\n\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ThreadData(tc.data)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPostBody(t *testing.T) {
	testCases := []struct {
		name     string
		post     *model.Post
		expected string
	}{
		{
			name: "post with no attachments",
			post: &model.Post{
				Message: "This is a test message",
			},
			expected: "This is a test message",
		},
		{
			name: "post with attachments",
			post: &model.Post{
				Message: "Message with attachments",
				Props: map[string]any{
					"attachments": []any{
						map[string]any{
							"pretext": "Pretext content",
							"title":   "Attachment title",
							"text":    "Attachment text",
							"fields": []any{
								map[string]any{
									"title": "Field1",
									"value": "Value1",
								},
								map[string]any{
									"title": "Field2",
									"value": 42,
								},
							},
							"footer": "Footer text",
						},
					},
				},
			},
			expected: `Message with attachments
Pretext content
Attachment title
Attachment text
Field1: "Value1"
Field2: 42
Footer text
`,
		},
		{
			name: "post with partial and multiple attachment fields",
			post: &model.Post{
				Message: "Partial fields",
				Props: map[string]any{
					"attachments": []any{
						map[string]any{
							"title": "Title only",
						},
						map[string]any{
							"text": "Text only",
						},
						map[string]any{
							"pretext": "Pretext only",
						},
						map[string]any{
							"footer": "Footer only",
						},
					},
				},
			},
			expected: `Partial fields
Title only

Text only

Pretext only

Footer only
`,
		},
		{
			name: "post with fields",
			post: &model.Post{
				Message: "Message with fields",
				Props: map[string]any{
					"attachments": []any{
						map[string]any{
							"fields": []any{
								map[string]any{
									"title": "Valid field",
									"value": "Valid value",
								},
							},
						},
					},
				},
			},
			expected: `Message with fields
Valid field: "Valid value"
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := PostBody(tc.post)
			assert.Equal(t, tc.expected, result)
		})
	}
}
