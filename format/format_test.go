// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{
			name: "channel with role admin",
			entry: ChannelEntry{
				Channel: &model.Channel{
					Id:          "ch1",
					Name:        "test",
					DisplayName: "Test",
					Type:        model.ChannelTypeOpen,
				},
				MemberCount: -1,
				Role:        "admin",
			},
			expected: "ID: ch1\nName: test\nDisplay Name: Test\nType: O\nYour role: admin\n\n",
		},
		{
			name: "channel with role not_member",
			entry: ChannelEntry{
				Channel: &model.Channel{
					Id:          "ch1",
					Name:        "test",
					DisplayName: "Test",
					Type:        model.ChannelTypeOpen,
				},
				MemberCount: -1,
				Role:        "not_member",
			},
			expected: "ID: ch1\nName: test\nDisplay Name: Test\nType: O\nYour role: not_member\n\n",
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

func TestListAgentsOutput(t *testing.T) {
	testCases := []struct {
		name     string
		out      mcptool.ListAgentsOutput
		expected string
	}{
		{
			name:     "empty",
			out:      mcptool.ListAgentsOutput{},
			expected: "No agents are currently configured.",
		},
		{
			name: "two agents no current",
			out: mcptool.ListAgentsOutput{
				Agents: []mcptool.AgentInfo{
					{ID: "bot1id12345678901234567", DisplayName: "Otto", Username: "otto"},
					{ID: "bot2id12345678901234567", DisplayName: "Claude", Username: "claude"},
				},
			},
			expected: `Found 2 agent(s):

1. Otto
   ID: bot1id12345678901234567
   Username: @otto

2. Claude
   ID: bot2id12345678901234567
   Username: @claude

`,
		},
		{
			name: "marks current agent",
			out: mcptool.ListAgentsOutput{
				Agents: []mcptool.AgentInfo{
					{ID: "bot1id12345678901234567", DisplayName: "Otto", Username: "otto"},
				},
				CurrentBotUserID: "bot1id12345678901234567",
			},
			expected: `Found 1 agent(s):

1. Otto
   ID: bot1id12345678901234567
   Username: @otto
   ** This is YOU (the current agent) **

`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ListAgentsOutput(tc.out)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestFormatters_AlwaysAppendPluginAnnotations exercises every public *Output
// formatter in this package with a DTO whose PluginAnnotations contain a unique
// sentinel string, and asserts the sentinel appears in the rendered output.
//
// When you add a new format.XxxOutput function, add a case here so we catch
// any return path that forgets to call AppendPluginAnnotations.
func TestFormatters_AlwaysAppendPluginAnnotations(t *testing.T) {
	const sentinel = "PLUGIN_ANNOTATION_SENTINEL_F8C2A3"
	anns := []string{sentinel}

	cases := map[string]func(t *testing.T) string{
		"SearchUsersOutput": func(t *testing.T) string {
			s, err := SearchUsersOutput(mcptool.SearchUsersOutput{
				Term:              "alice",
				Users:             []*model.User{{Id: "u1", Username: "alice"}},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"SearchPostsOutput": func(t *testing.T) string {
			s, err := SearchPostsOutput(mcptool.SearchPostsOutput{
				Query: "hello",
				KeywordResults: []mcptool.SearchPostResult{
					{Post: &model.Post{Id: "p1", ChannelId: "c1", Message: "hi"}, Username: "alice", Source: "keyword"},
				},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ReadChannelOutput": func(t *testing.T) string {
			s, err := ReadChannelOutput(mcptool.ReadChannelOutput{
				Channel:           &model.Channel{Id: "c1", DisplayName: "General"},
				Posts:             []*model.Post{{Id: "p1", UserId: "u1", Message: "hi"}},
				Users:             map[string]*model.User{"u1": {Id: "u1", Username: "alice"}},
				TeamName:          "Eng",
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ReadChannelOutput_RedactedEmpty": func(t *testing.T) string {
			s, err := ReadChannelOutput(mcptool.ReadChannelOutput{
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ReadPostOutput": func(t *testing.T) string {
			s, err := ReadPostOutput(mcptool.ReadPostOutput{
				Posts:             []*model.Post{{Id: "p1", ChannelId: "c1", UserId: "u1", Message: "hi"}},
				Users:             map[string]*model.User{"u1": {Id: "u1", Username: "alice"}},
				ChannelName:       "General",
				TeamName:          "Eng",
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ChannelInfoOutput": func(t *testing.T) string {
			s, err := ChannelInfoOutput(mcptool.ChannelInfoOutput{
				Channels:          []*model.Channel{{Id: "c1", DisplayName: "General", TeamId: "t1"}},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ChannelInfoOutput_RedactedEmpty": func(t *testing.T) string {
			s, err := ChannelInfoOutput(mcptool.ChannelInfoOutput{
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ChannelInfoOutput_MultipleMatches": func(t *testing.T) string {
			s, err := ChannelInfoOutput(mcptool.ChannelInfoOutput{
				Channels: []*model.Channel{
					{Id: "c1", DisplayName: "General", TeamId: "t1"},
					{Id: "c2", DisplayName: "General", TeamId: "t2"},
				},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ChannelMembersOutput": func(t *testing.T) string {
			s, err := ChannelMembersOutput(mcptool.ChannelMembersOutput{
				Channel:           &model.Channel{Id: "c1", DisplayName: "General"},
				Rows:              []mcptool.ChannelMemberRow{{User: &model.User{Id: "u1", Username: "alice"}, SchemeUser: true}},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"UserChannelsOutput": func(t *testing.T) string {
			s, err := UserChannelsOutput(mcptool.UserChannelsOutput{
				Channels:          []*model.Channel{{Id: "c1", DisplayName: "General", TeamId: "t1"}},
				PageInfo:          mcptool.UserChannelsPageInfo{Page: 0, PerPage: 60, TotalCount: 1},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"CreateChannelOutput": func(t *testing.T) string {
			s, err := CreateChannelOutput(mcptool.CreateChannelOutput{
				Channel:           &model.Channel{Id: "c1", DisplayName: "General"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"AddUserToChannelOutput": func(t *testing.T) string {
			s, err := AddUserToChannelOutput(mcptool.AddUserToChannelOutput{
				UserID:            "u1",
				ChannelID:         "c1",
				User:              &model.User{Id: "u1", Username: "alice"},
				Channel:           &model.Channel{Id: "c1", DisplayName: "General"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"CreatePostOutput": func(t *testing.T) string {
			s, err := CreatePostOutput(mcptool.CreatePostOutput{
				Post:              &model.Post{Id: "p1", ChannelId: "c1"},
				Channel:           &model.Channel{Id: "c1", DisplayName: "General"},
				Team:              &model.Team{Id: "t1", DisplayName: "Eng"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"CreatePostAsUserOutput": func(t *testing.T) string {
			s, err := CreatePostAsUserOutput(mcptool.CreatePostAsUserOutput{
				Post:              &model.Post{Id: "p1", ChannelId: "c1"},
				Username:          "alice",
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"DMOutput": func(t *testing.T) string {
			s, err := DMOutput(mcptool.DMOutput{
				Post:              &model.Post{Id: "p1", ChannelId: "c1"},
				TargetUser:        &model.User{Id: "u1", Username: "bob"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"GroupMessageOutput": func(t *testing.T) string {
			s, err := GroupMessageOutput(mcptool.GroupMessageOutput{
				Post:              &model.Post{Id: "p1", ChannelId: "c1"},
				Usernames:         []string{"alice", "bob"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"TeamInfoOutput": func(t *testing.T) string {
			s, err := TeamInfoOutput(mcptool.TeamInfoOutput{
				Teams:             []*model.Team{{Id: "t1", DisplayName: "Eng"}},
				MemberCount:       3,
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"TeamInfoOutput_RedactedEmpty": func(t *testing.T) string {
			s, err := TeamInfoOutput(mcptool.TeamInfoOutput{
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"TeamInfoOutput_MultipleMatches": func(t *testing.T) string {
			s, err := TeamInfoOutput(mcptool.TeamInfoOutput{
				Teams: []*model.Team{
					{Id: "t1", DisplayName: "Engineering", Name: "engineering"},
					{Id: "t2", DisplayName: "Eng Ops", Name: "eng-ops"},
				},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"TeamMembersOutput": func(t *testing.T) string {
			s, err := TeamMembersOutput(mcptool.TeamMembersOutput{
				Rows:              []mcptool.TeamMemberRow{{User: &model.User{Id: "u1", Username: "alice"}, SchemeUser: true}},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"CreateTeamOutput": func(t *testing.T) string {
			s, err := CreateTeamOutput(mcptool.CreateTeamOutput{
				Team:              &model.Team{Id: "t1", DisplayName: "Eng"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"AddUserToTeamOutput": func(t *testing.T) string {
			s, err := AddUserToTeamOutput(mcptool.AddUserToTeamOutput{
				UserID:            "u1",
				TeamID:            "t1",
				User:              &model.User{Id: "u1", Username: "alice"},
				Team:              &model.Team{Id: "t1", DisplayName: "Eng"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"CreateUserOutput": func(t *testing.T) string {
			s, err := CreateUserOutput(mcptool.CreateUserOutput{
				User:              &model.User{Id: "u1", Username: "alice"},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
		"ListAgentsOutput": func(t *testing.T) string {
			s, err := ListAgentsOutput(mcptool.ListAgentsOutput{
				Agents:            []mcptool.AgentInfo{{ID: "u1", DisplayName: "Otto", Username: "otto"}},
				PluginAnnotations: anns,
			})
			require.NoError(t, err)
			return s
		},
	}

	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			out := fn(t)
			assert.Contains(t, out, sentinel,
				"formatter %q must include plugin annotations in its output (call AppendPluginAnnotations on every return path)", name)
		})
	}
}
