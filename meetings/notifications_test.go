// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recordingNotifier marks posts like the real service and records run ends.
type recordingNotifier struct {
	*notifications.Service
	ends chan notifications.StreamEnd
}

func (n *recordingNotifier) Notify(_ context.Context, end notifications.StreamEnd) {
	n.ends <- end
}

func TestCallRecordingPostsNotifications(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		supported            bool
		wantProgressDeferred bool
	}{
		{name: "server defers notifications", supported: true, wantProgressDeferred: true},
		{name: "server cannot defer notifications", supported: false, wantProgressDeferred: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const (
				botID       = "botid000000000000000000000"
				requesterID = "requester00000000000000000"
				dmID        = "dmchannel00000000000000000"
			)
			siteURL := "http://localhost:8065"

			var mu sync.Mutex
			var created []*model.Post

			api := &plugintest.API{}
			api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &siteURL}})
			api.On("GetDirectChannel", botID, requesterID).Return(&model.Channel{Id: dmID, Type: model.ChannelTypeDirect}, nil)
			api.On("CreatePost", mock.Anything).Return(func(post *model.Post) (*model.Post, *model.AppError) {
				mu.Lock()
				defer mu.Unlock()
				saved := post.Clone()
				saved.Id = model.NewId()
				created = append(created, saved.Clone())
				return saved, nil
			})
			api.On("UpdatePost", mock.Anything).Return(func(post *model.Post) (*model.Post, *model.AppError) {
				return post.Clone(), nil
			})
			api.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()
			defer api.AssertExpectations(t)

			notifier := &recordingNotifier{
				Service: notifications.NewService(nil, nil, tc.supported),
				ends:    make(chan notifications.StreamEnd, 1),
			}
			s := &Service{
				pluginAPI:      pluginapi.NewClient(api, nil),
				i18n:           i18n.Init(),
				licenseChecker: enterprisetest.CheckerAt(enterprise.LevelEnterprise),
				notifier:       notifier,
			}
			bot := bots.NewBot(llm.BotConfig{}, llm.ServiceConfig{}, &model.Bot{UserId: botID}, nil)

			surePost, err := s.newCallRecordingThread(bot, &model.User{Id: requesterID}, &model.Post{Id: model.NewId()}, &model.Channel{Id: model.NewId()}, model.NewId(), nil)
			require.NoError(t, err)

			// Transcribing fails at once because ffmpeg is not configured.
			var end notifications.StreamEnd
			select {
			case end = <-notifier.ends:
			case <-time.After(5 * time.Second):
				t.Fatal("the failed summary never notified")
			}

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, created, 2)
			acknowledgement, progress := created[0], created[1]
			require.Equal(t, surePost.Id, acknowledgement.Id)
			require.Equal(t, surePost.Id, progress.RootId)

			assert.Equal(t, true, acknowledgement.GetProp(model.PostPropsSilentNotification), "the acknowledgement answers the user's own request")
			assert.False(t, acknowledgement.HasDeferredNotification())

			assert.Equal(t, tc.wantProgressDeferred, progress.HasDeferredNotification(), "the summary is streamed into the progress post")
			assert.Nil(t, progress.GetProp(model.PostPropsSilentNotification))

			assert.Equal(t, notifications.StreamEnd{
				PostID:       progress.Id,
				ChannelID:    dmID,
				RequesterID:  requesterID,
				Outcome:      notifications.OutcomeErrored,
				PostDeferred: tc.wantProgressDeferred,
			}, end)
			assert.Equal(t, notifications.DeliveryRequesterOnly, notifications.Decide(end))
		})
	}
}
