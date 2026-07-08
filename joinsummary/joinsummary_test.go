// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package joinsummary

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestShouldSummarizeChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel *model.Channel
		want    bool
	}{
		{
			name:    "nil channel",
			channel: nil,
			want:    false,
		},
		{
			name:    "public channel",
			channel: &model.Channel{Type: model.ChannelTypeOpen},
			want:    true,
		},
		{
			name:    "private channel",
			channel: &model.Channel{Type: model.ChannelTypePrivate},
			want:    true,
		},
		{
			name:    "direct message",
			channel: &model.Channel{Type: model.ChannelTypeDirect},
			want:    false,
		},
		{
			name:    "group message",
			channel: &model.Channel{Type: model.ChannelTypeGroup},
			want:    false,
		},
		{
			name:    "archived public channel",
			channel: &model.Channel{Type: model.ChannelTypeOpen, DeleteAt: 123},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSummarizeChannel(tt.channel); got != tt.want {
				t.Errorf("shouldSummarizeChannel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterSummarizablePosts(t *testing.T) {
	tests := []struct {
		name    string
		posts   []*model.Post
		wantIDs []string
	}{
		{
			name:    "empty",
			posts:   nil,
			wantIDs: []string{},
		},
		{
			name: "drops deleted and system posts",
			posts: []*model.Post{
				{Id: "keep1", Message: "hello"},
				{Id: "deleted", Message: "gone", DeleteAt: 999},
				{Id: "system", Message: "joined the channel", Type: model.PostTypeJoinChannel},
				{Id: "keep2", Message: "world"},
			},
			wantIDs: []string{"keep1", "keep2"},
		},
		{
			name: "drops nil entries",
			posts: []*model.Post{
				nil,
				{Id: "keep", Message: "hi"},
			},
			wantIDs: []string{"keep"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterSummarizablePosts(tt.posts)
			gotIDs := make([]string, 0, len(got))
			for _, p := range got {
				gotIDs = append(gotIDs, p.Id)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("filterSummarizablePosts() = %v, want %v", gotIDs, tt.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Errorf("filterSummarizablePosts()[%d] = %q, want %q", i, gotIDs[i], tt.wantIDs[i])
				}
			}
		})
	}
}

func TestResolveDefaults(t *testing.T) {
	tests := []struct {
		name         string
		lookbackIn   int
		lookbackWant int
		minPostsIn   int
		minPostsWant int
	}{
		{"zero falls back to defaults", 0, defaultLookbackDays, 0, defaultMinPosts},
		{"negative falls back to defaults", -5, defaultLookbackDays, -1, defaultMinPosts},
		{"positive kept", 14, 14, 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLookbackDays(tt.lookbackIn); got != tt.lookbackWant {
				t.Errorf("resolveLookbackDays(%d) = %d, want %d", tt.lookbackIn, got, tt.lookbackWant)
			}
			if got := resolveMinPosts(tt.minPostsIn); got != tt.minPostsWant {
				t.Errorf("resolveMinPosts(%d) = %d, want %d", tt.minPostsIn, got, tt.minPostsWant)
			}
		})
	}
}

func TestChannelName(t *testing.T) {
	tests := []struct {
		name    string
		channel *model.Channel
		want    string
	}{
		{"display name preferred", &model.Channel{DisplayName: "Engineering", Name: "engineering"}, "Engineering"},
		{"falls back to name", &model.Channel{DisplayName: "", Name: "engineering"}, "engineering"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelName(tt.channel); got != tt.want {
				t.Errorf("channelName() = %q, want %q", got, tt.want)
			}
		})
	}
}
