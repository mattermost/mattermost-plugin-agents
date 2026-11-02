// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestParseAmbientClassifierOutput(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    bool
		wantErr bool
	}{
		{name: "true", raw: `{"should_reply":true}`, want: true},
		{name: "false", raw: `{"should_reply":false}`, want: false},
		{name: "fenced true", raw: "```json\n{\"should_reply\":true}\n```", want: true},
		{name: "malformed", raw: `{not json`, wantErr: true},
		{name: "missing field", raw: `{"other":true}`, wantErr: true},
		{name: "wrong type", raw: `{"should_reply":"yes"}`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAmbientClassifierOutput(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNewestPostsIncluding(t *testing.T) {
	thread := make([]*model.Post, 25)
	for i := range thread {
		thread[i] = &model.Post{Id: fmt.Sprintf("p%d", i), CreateAt: int64(i)}
	}

	tests := []struct {
		name      string
		thread    []*model.Post
		trigger   *model.Post
		wantFirst string
		wantLast  string
	}{
		{name: "short thread is kept whole", thread: thread[:3], trigger: thread[2], wantFirst: "p0", wantLast: "p2"},
		{name: "keeps the newest posts when the trigger is newest", thread: thread, trigger: thread[24], wantFirst: "p5", wantLast: "p24"},
		{name: "an older trigger replaces the oldest kept post", thread: thread, trigger: thread[1], wantFirst: "p1", wantLast: "p24"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := newestPostsIncluding(tc.thread, tc.trigger, ambientClassifierMaxPosts)
			require.LessOrEqual(t, len(got), ambientClassifierMaxPosts)
			require.Equal(t, tc.wantFirst, got[0].Id)
			require.Equal(t, tc.wantLast, got[len(got)-1].Id)
		})
	}
}

func TestFormatAmbientThread(t *testing.T) {
	users := map[string]*model.User{"u": {Id: "u", Username: "alice"}}
	post := func(id, msg string, createAt int64) *model.Post {
		return &model.Post{Id: id, UserId: "u", Message: msg, CreateAt: createAt}
	}
	filler := strings.Repeat("Ö", ambientClassifierMaxThreadRunes)

	tests := []struct {
		name      string
		posts     []*model.Post
		triggerID string
		// wantFrontTrim asserts the output equals the whole thread with its
		// oldest runes trimmed.
		wantFrontTrim bool
		wantContains  []string
		wantOmits     []string
	}{
		{
			name:          "short thread is kept whole",
			posts:         []*model.Post{post("a", "FIRST", 1), post("b", "SECOND", 2)},
			triggerID:     "b",
			wantFrontTrim: true,
			wantContains:  []string{"FIRST", "SECOND"},
		},
		{
			name:          "newest trigger keeps the newest runes of the thread",
			posts:         []*model.Post{post("old", "OLDEST-HEAD"+filler, 1), post("new", "NEWEST-TAIL", 2)},
			triggerID:     "new",
			wantFrontTrim: true,
			wantContains:  []string{"NEWEST-TAIL"},
			wantOmits:     []string{"OLDEST-HEAD"},
		},
		{
			name:         "an older trigger is kept ahead of newer posts that fill the budget",
			posts:        []*model.Post{post("trigger", "TRIGGER-MARKER", 1), post("mid", "MIDDLE-HEAD"+filler, 2), post("new", "NEWEST-TAIL", 3)},
			triggerID:    "trigger",
			wantContains: []string{"TRIGGER-MARKER", "NEWEST-TAIL"},
			wantOmits:    []string{"MIDDLE-HEAD"},
		},
		{
			name:         "a trigger over the budget keeps its newest runes and crowds out the rest",
			posts:        []*model.Post{post("old", "OTHER-POST", 1), post("trigger", "TRIGGER-HEAD"+filler+"TRIGGER-TAIL", 2)},
			triggerID:    "trigger",
			wantContains: []string{"TRIGGER-TAIL"},
			wantOmits:    []string{"TRIGGER-HEAD", "OTHER-POST"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := &mmapi.ThreadData{Posts: tc.posts, UsersByID: users}
			formatted := formatAmbientThread(data, tc.triggerID)

			require.LessOrEqual(t, utf8.RuneCountInString(formatted), ambientClassifierMaxThreadRunes)
			require.True(t, utf8.ValidString(formatted))
			if tc.wantFrontTrim {
				require.Equal(t, trimRunesKeepNewest(format.ThreadData(data), ambientClassifierMaxThreadRunes), formatted)
			}
			for _, needle := range tc.wantContains {
				require.Contains(t, formatted, needle)
			}
			for _, needle := range tc.wantOmits {
				require.NotContains(t, formatted, needle)
			}
		})
	}
}

func TestAmbientThreadPosts(t *testing.T) {
	root := &model.Post{Id: "root", UserId: "u1", CreateAt: 1}
	reply := &model.Post{Id: "reply", UserId: "u2", RootId: "root", CreateAt: 2}

	tests := []struct {
		name    string
		trigger *model.Post
		fetched *model.PostList // nil means no fetch is expected
		want    []*model.Post
	}{
		{name: "a top-level post is its own thread without a fetch", trigger: root, want: []*model.Post{root}},
		{
			name:    "a reply missing from the fetched thread is appended in order",
			trigger: reply,
			fetched: &model.PostList{Order: []string{"root"}, Posts: map[string]*model.Post{"root": root}},
			want:    []*model.Post{root, reply},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mmClient := mmapimocks.NewMockClient(t)
			if tc.fetched != nil {
				mmClient.EXPECT().GetPostThread(tc.trigger.RootId).Return(tc.fetched, nil)
			}
			c := &Conversations{mmClient: mmClient}

			got, err := c.ambientThreadPosts(tc.trigger)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestClassifyAmbientTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		post := &model.Post{Id: "p1", UserId: "u1", Message: "hi", CreateAt: 1}
		mmClient := mmapimocks.NewMockClient(t)
		mmClient.EXPECT().GetUser("u1").Return(&model.User{Id: "u1", Username: "alice"}, nil)
		mmClient.EXPECT().LogWarn(mock.Anything, mock.Anything).Once()

		promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
		require.NoError(t, err)

		bot := bots.NewBot(
			llm.BotConfig{ID: "b1", Name: "bot"},
			llm.ServiceConfig{},
			&model.Bot{UserId: "b1", Username: "bot"},
			&blockingNoStreamLLM{},
		)
		mockAPI := &plugintest.API{}
		mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
		mockAPI.On("GetLicense").Return(&model.License{}).Maybe()
		mockAPI.On("GetTeam", "t1").Return(&model.Team{Id: "t1"}, nil).Maybe()
		contextBuilder := llmcontext.NewLLMContextBuilder(pluginapi.NewClient(mockAPI, nil), nil, nil, nil)
		c := &Conversations{prompts: promptsManager, mmClient: mmClient, contextBuilder: contextBuilder}

		user := &model.User{Id: "u1", Username: "alice"}
		channel := &model.Channel{Id: "c1", TeamId: "t1", Type: model.ChannelTypeOpen}
		start := time.Now()
		err = c.classifyAmbientReply(context.Background(), bot, &autoreply.Setting{Mode: autoreply.ModeAmbient}, post, user, channel, []*model.Post{post})
		require.ErrorIs(t, err, ErrNoResponse)
		require.ErrorContains(t, err, context.DeadlineExceeded.Error())
		require.Equal(t, ambientClassifierTimeout, time.Since(start), "the classifier must give up exactly at its timeout")
	})
}

type blockingNoStreamLLM struct{}

func (l *blockingNoStreamLLM) ChatCompletion(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return nil, errors.New("unused")
}

func (l *blockingNoStreamLLM) ChatCompletionNoStream(ctx context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (l *blockingNoStreamLLM) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}

func (l *blockingNoStreamLLM) InputTokenLimit() int  { return 100000 }
func (l *blockingNoStreamLLM) OutputTokenLimit() int { return 8192 }
