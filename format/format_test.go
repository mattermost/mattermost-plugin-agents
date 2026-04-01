// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/mmapi"
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
			name: "posts with timestamps",
			data: &mmapi.ThreadData{
				Posts: []*model.Post{
					{
						UserId:   "user1",
						Message:  "Morning update",
						CreateAt: 1710878490000, // 2024-03-19T20:01:30Z
					},
					{
						UserId:   "user2",
						Message:  "Thanks",
						CreateAt: 1710878492000,
					},
				},
				UsersByID: map[string]*model.User{
					"user1": {Username: "johndoe"},
					"user2": {Username: "janedoe"},
				},
			},
			expected: "johndoe (2024-03-19T20:01:30Z): Morning update\n\njanedoe (2024-03-19T20:01:32Z): Thanks\n\n",
		},
		{
			name: "post with user not in UsersByID map",
			data: &mmapi.ThreadData{
				Posts: []*model.Post{
					{
						UserId:  "missing-user",
						Message: "Orphaned message",
					},
				},
				UsersByID: map[string]*model.User{},
			},
			expected: "unknown: Orphaned message\n\n",
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

func TestFormatPost(t *testing.T) {
	tests := []struct {
		name     string
		entry    PostEntry
		expected string
	}{
		{
			name: "basic post with timestamp",
			entry: PostEntry{
				HeaderLabel: "Post 1",
				Username:    "alice",
				Post: &model.Post{
					Id:       "post1",
					Message:  "Hello world",
					CreateAt: 1710878490000, // 2024-03-19T20:01:30Z
				},
			},
			expected: "**Post 1** by alice:\nPost ID: post1\nTime: 2024-03-19T20:01:30Z\nMessage: Hello world\n\n",
		},
		{
			name: "reply with annotation and root ID",
			entry: PostEntry{
				HeaderLabel:     "Post 3",
				Username:        "alice",
				ReplyAnnotation: "(reply to Post 2)",
				Post: &model.Post{
					Id:       "post3",
					RootId:   "post2",
					Message:  "Next sprint",
					CreateAt: 1710878492000,
				},
			},
			expected: "**Post 3** by alice (reply to Post 2):\nPost ID: post3\nRoot ID: post2\nTime: 2024-03-19T20:01:32Z\nMessage: Next sprint\n\n",
		},
		{
			name: "search result with score and channel",
			entry: PostEntry{
				HeaderLabel: "Result 1",
				Username:    "@alice",
				Score:       0.95,
				Post:        &model.Post{Id: "post1", ChannelId: "ch1", Message: "Found it"},
				ChannelName: "General",
				TeamName:    "Engineering",
				ShowChannel: true,
			},
			expected: "**Result 1** (Score: 0.95) by @alice:\nChannel: General (Team: Engineering)\nPost ID: post1\nChannel ID: ch1\nMessage: Found it\n\n",
		},
		{
			name: "unknown user fallback",
			entry: PostEntry{
				HeaderLabel: "Post 1",
				Username:    "",
				Post:        &model.Post{Id: "post1", Message: "Orphaned"},
			},
			expected: "**Post 1** by Unknown User:\nPost ID: post1\nMessage: Orphaned\n\n",
		},
		{
			name: "no timestamp when CreateAt is zero",
			entry: PostEntry{
				HeaderLabel: "Post 1",
				Username:    "bob",
				Post:        &model.Post{Id: "post1", Message: "No time"},
			},
			expected: "**Post 1** by bob:\nPost ID: post1\nMessage: No time\n\n",
		},
		{
			name: "channel name without team",
			entry: PostEntry{
				HeaderLabel: "Result 1",
				Username:    "@bob",
				Post:        &model.Post{Id: "post1", Message: "DM content"},
				ChannelName: "Direct Message",
			},
			expected: "**Result 1** by @bob:\nChannel: Direct Message\nPost ID: post1\nMessage: DM content\n\n",
		},
		{
			name: "post with attachments uses PostBody",
			entry: PostEntry{
				HeaderLabel: "Post 1",
				Username:    "charlie",
				Post: &model.Post{
					Id:      "post1",
					Message: "See attached",
					Props: map[string]any{
						"attachments": []any{
							map[string]any{
								"title": "Report",
								"text":  "Q4 numbers",
							},
						},
					},
				},
			},
			expected: "**Post 1** by charlie:\nPost ID: post1\nMessage: See attached\nReport\nQ4 numbers\n\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			WritePost(&buf, tt.entry)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestMemberRole(t *testing.T) {
	tests := []struct {
		name                                 string
		schemeAdmin, schemeGuest, schemeUser bool
		expected                             string
	}{
		{"admin", true, false, true, "admin"},
		{"guest", false, true, false, "guest"},
		{"member", false, false, true, "member"},
		{"no role", false, false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, MemberRole(tt.schemeAdmin, tt.schemeGuest, tt.schemeUser))
		})
	}
}

func TestWriteUser(t *testing.T) {
	tests := []struct {
		name     string
		entry    UserEntry
		expected string
	}{
		{
			name: "search result with header",
			entry: UserEntry{
				HeaderLabel: "User 1",
				User: &model.User{
					Id:        "u1",
					Username:  "alice",
					FirstName: "Alice",
					LastName:  "Smith",
					Email:     "alice@example.com",
					Nickname:  "Ali",
					Position:  "Engineer",
				},
			},
			expected: "**User 1**:\nUsername: alice\nID: u1\nName: Alice Smith\nEmail: alice@example.com\nNickname: Ali\nPosition: Engineer\n\n",
		},
		{
			name: "member list without header",
			entry: UserEntry{
				User: &model.User{
					Id:        "u1",
					Username:  "bob",
					FirstName: "Bob",
					LastName:  "Jones",
					Email:     "bob@example.com",
				},
				Role: "admin",
			},
			expected: "Username: bob\nID: u1\nName: Bob Jones\nEmail: bob@example.com\nRole: admin\n\n",
		},
		{
			name: "bot user",
			entry: UserEntry{
				User: &model.User{
					Id:       "u1",
					Username: "webhook-bot",
					IsBot:    true,
				},
				Role: "member",
			},
			expected: "Username: webhook-bot\nID: u1\nIs Bot: true\nRole: member\n\n",
		},
		{
			name: "user with only last name",
			entry: UserEntry{
				User: &model.User{
					Id:       "u1",
					Username: "jsmith",
					LastName: "Smith",
				},
			},
			expected: "Username: jsmith\nID: u1\nName: Smith\n\n",
		},
		{
			name: "user with only first name",
			entry: UserEntry{
				User: &model.User{
					Id:        "u1",
					Username:  "john",
					FirstName: "John",
				},
			},
			expected: "Username: john\nID: u1\nName: John\n\n",
		},
		{
			name: "deactivated user",
			entry: UserEntry{
				User: &model.User{
					Id:       "u1",
					Username: "departed",
					DeleteAt: 1710878490000,
				},
			},
			expected: "Username: departed\nID: u1\nDeactivated: true\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			WriteUser(&buf, tt.entry)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestWriteChannel(t *testing.T) {
	tests := []struct {
		name     string
		entry    ChannelEntry
		expected string
	}{
		{
			name: "full channel info",
			entry: ChannelEntry{
				HeaderLabel: "Channel Information:",
				Channel: &model.Channel{
					Id:          "ch1",
					Name:        "general",
					DisplayName: "General",
					Type:        model.ChannelTypeOpen,
					TeamId:      "team1",
					Purpose:     "General discussion",
					Header:      "Welcome!",
					CreateAt:    1710878490000,
				},
				TeamName:    "Engineering",
				MemberCount: 45,
			},
			expected: "Channel Information:\nID: ch1\nName: general\nDisplay Name: General\nType: O\nTeam: Engineering (ID: team1)\nPurpose: General discussion\nHeader: Welcome!\nCreated: 2024-03-19T20:01:30Z\nMember Count: 45\n\n",
		},
		{
			name: "channel without optional fields",
			entry: ChannelEntry{
				Channel: &model.Channel{
					Id:          "ch1",
					Name:        "test",
					DisplayName: "Test",
					Type:        model.ChannelTypePrivate,
				},
				MemberCount: -1,
			},
			expected: "ID: ch1\nName: test\nDisplay Name: Test\nType: P\n\n",
		},
		{
			name: "channel with team ID only",
			entry: ChannelEntry{
				Channel: &model.Channel{
					Id:          "ch1",
					Name:        "test",
					DisplayName: "Test",
					Type:        model.ChannelTypeOpen,
					TeamId:      "team1",
				},
				TeamID:      "team1",
				MemberCount: -1,
			},
			expected: "ID: ch1\nName: test\nDisplay Name: Test\nType: O\nTeam ID: team1\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			WriteChannel(&buf, tt.entry)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestWriteTeam(t *testing.T) {
	tests := []struct {
		name     string
		entry    TeamEntry
		expected string
	}{
		{
			name: "full team info",
			entry: TeamEntry{
				Team: &model.Team{
					Id:          "team1",
					Name:        "engineering",
					DisplayName: "Engineering",
					Type:        model.TeamOpen,
					Description: "Engineering org",
					CreateAt:    1710878490000,
				},
				MemberCount: 120,
			},
			expected: "Team Information:\nID: team1\nName: engineering\nDisplay Name: Engineering\nType: O\nDescription: Engineering org\nCreated: 2024-03-19T20:01:30Z\nMember Count: 120\n",
		},
		{
			name: "team without description",
			entry: TeamEntry{
				Team: &model.Team{
					Id:          "team1",
					Name:        "product",
					DisplayName: "Product",
					Type:        model.TeamInvite,
				},
				MemberCount: -1,
			},
			expected: "Team Information:\nID: team1\nName: product\nDisplay Name: Product\nType: I\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			WriteTeam(&buf, tt.entry)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}
