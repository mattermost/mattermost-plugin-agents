// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestStripTrustedProps(t *testing.T) {
	const botID = "agent-bot"
	isAgentBot := func(id string) bool { return id == botID }

	forged := func(userID string) *model.Post {
		p := &model.Post{UserId: userID, Message: "hi"}
		for _, key := range pluginTrustedPostProps {
			p.AddProp(key, "forged")
		}
		p.AddProp("from_webhook", "true")
		p.AddProp("unsafe_links", "true")
		return p
	}

	tests := []struct {
		name        string
		post        *model.Post
		wantTrusted bool
	}{
		{name: "user post loses plugin-trusted props", post: forged("user"), wantTrusted: false},
		{name: "agent bot post keeps them", post: forged(botID), wantTrusted: true},
		{name: "other bot/integration post loses them", post: forged("other-bot"), wantTrusted: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripTrustedProps(tc.post, isAgentBot)
			for _, key := range pluginTrustedPostProps {
				if tc.wantTrusted {
					assert.NotNil(t, got.GetProp(key), key)
				} else {
					assert.Nil(t, got.GetProp(key), key)
				}
			}
			// Unrelated props and content are never touched.
			assert.Equal(t, "true", got.GetProp("from_webhook"))
			assert.Equal(t, "true", got.GetProp("unsafe_links"))
			assert.Equal(t, "hi", got.Message)
		})
	}

	t.Run("post without trusted props is returned unchanged", func(t *testing.T) {
		p := &model.Post{UserId: "user", Message: "plain"}
		p.AddProp("custom", "x")
		assert.Same(t, p, stripTrustedProps(p, isAgentBot))
	})
}
