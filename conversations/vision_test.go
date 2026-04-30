// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestVisionFileIDs(t *testing.T) {
	visionBot := bots.NewBot(llm.BotConfig{EnableVision: true}, llm.ServiceConfig{}, nil, nil)
	textBot := bots.NewBot(llm.BotConfig{EnableVision: false}, llm.ServiceConfig{}, nil, nil)

	tests := []struct {
		name string
		bot  *bots.Bot
		post *model.Post
		want []string
	}{
		{
			name: "vision enabled includes uploaded file IDs",
			bot:  visionBot,
			post: &model.Post{FileIds: []string{"file1", "file2"}},
			want: []string{"file1", "file2"},
		},
		{
			name: "vision disabled omits uploaded file IDs",
			bot:  textBot,
			post: &model.Post{FileIds: []string{"file1"}},
			want: nil,
		},
		{
			name: "no files",
			bot:  visionBot,
			post: &model.Post{},
			want: nil,
		},
		{
			name: "nil bot",
			bot:  nil,
			post: &model.Post{FileIds: []string{"file1"}},
			want: nil,
		},
		{
			name: "nil post",
			bot:  visionBot,
			post: nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := visionFileIDs(tt.bot, tt.post)
			assert.Equal(t, tt.want, got)
			if len(got) > 0 {
				tt.post.FileIds[0] = "mutated"
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
