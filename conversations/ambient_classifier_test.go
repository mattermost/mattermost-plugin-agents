// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

func TestFormatAmbientThreadKeepsNewestRunes(t *testing.T) {
	users := map[string]*model.User{"u": {Id: "u", Username: "alice"}}
	old := &model.Post{Id: "old", UserId: "u", Message: strings.Repeat("Ö", ambientClassifierMaxThreadRunes), CreateAt: 1}
	newest := &model.Post{Id: "new", UserId: "u", Message: "NEWEST-TAIL", CreateAt: 2}

	formatted := formatAmbientThread(&mmapi.ThreadData{Posts: []*model.Post{old, newest}, UsersByID: users})

	require.Equal(t, ambientClassifierMaxThreadRunes, utf8.RuneCountInString(formatted))
	require.True(t, utf8.ValidString(formatted))
	require.Contains(t, formatted, "NEWEST-TAIL")
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
	orig := ambientClassifierTimeout
	ambientClassifierTimeout = 20 * time.Millisecond
	t.Cleanup(func() { ambientClassifierTimeout = orig })

	post := &model.Post{Id: "p1", UserId: "u1", Message: "hi", CreateAt: 1}
	mmClient := mmapimocks.NewMockClient(t)
	mmClient.EXPECT().GetUser("u1").Return(&model.User{Id: "u1", Username: "alice"}, nil)

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
	err = c.classifyAmbientReply(context.Background(), bot, &autoreply.Setting{Mode: autoreply.ModeAmbient}, post, user, channel, []*model.Post{post})
	require.ErrorIs(t, err, ErrNoResponse)
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
