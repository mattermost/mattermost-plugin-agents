// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
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

func TestBoundAmbientThread(t *testing.T) {
	users := map[string]*model.User{"u": {Id: "u", Username: "alice"}}

	t.Run("keeps newest 20 including trigger", func(t *testing.T) {
		posts := make([]*model.Post, 25)
		for i := 0; i < 25; i++ {
			posts[i] = &model.Post{Id: string(rune('a' + i)), UserId: "u", Message: strings.Repeat("x", 3) + string(rune('0'+i%10)), CreateAt: int64(i)}
		}
		posts[0].Message = "OLDEST"
		posts[24].Message = "NEWEST"
		formatted := boundAmbientThread(&mmapi.ThreadData{Posts: posts, UsersByID: users}, posts[24])
		require.NotContains(t, formatted, "OLDEST")
		require.Contains(t, formatted, "NEWEST")
	})

	t.Run("includes trigger when missing from the fetch", func(t *testing.T) {
		existing := []*model.Post{{Id: "old", UserId: "u", Message: "existing", CreateAt: 1}}
		trigger := &model.Post{Id: "trig", UserId: "u", Message: "TRIGGER-POST", CreateAt: 2}
		formatted := boundAmbientThread(&mmapi.ThreadData{Posts: existing, UsersByID: users}, trigger)
		require.Contains(t, formatted, "TRIGGER-POST")
	})

	t.Run("trims formatted thread from the start keeping newest runes", func(t *testing.T) {
		old := &model.Post{Id: "old", UserId: "u", Message: strings.Repeat("O", ambientClassifierMaxThreadRunes), CreateAt: 1}
		newest := &model.Post{Id: "new", UserId: "u", Message: "NEWEST-TAIL", CreateAt: 2}
		formatted := boundAmbientThread(&mmapi.ThreadData{Posts: []*model.Post{old, newest}, UsersByID: users}, newest)
		require.LessOrEqual(t, utf8.RuneCountInString(formatted), ambientClassifierMaxThreadRunes)
		require.Contains(t, formatted, "NEWEST-TAIL")
	})
}

func TestClassifyAmbientTimeout(t *testing.T) {
	orig := ambientClassifierTimeout
	ambientClassifierTimeout = 20 * time.Millisecond
	t.Cleanup(func() { ambientClassifierTimeout = orig })

	post := &model.Post{Id: "p1", UserId: "u1", Message: "hi", CreateAt: 1}
	mmClient := mmapimocks.NewMockClient(t)
	mmClient.EXPECT().GetPostThread("p1").Return(&model.PostList{
		Order: []string{"p1"},
		Posts: map[string]*model.Post{"p1": post},
	}, nil)
	mmClient.EXPECT().GetUser("u1").Return(&model.User{Id: "u1", Username: "alice"}, nil)

	promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	bot := bots.NewBot(
		llm.BotConfig{ID: "b1", Name: "bot"},
		llm.ServiceConfig{},
		&model.Bot{UserId: "b1", Username: "bot"},
		&blockingNoStreamLLM{},
	)
	c := &Conversations{prompts: promptsManager, mmClient: mmClient}

	err = c.classifyAmbientReply(context.Background(), bot, &autoreply.Setting{Mode: autoreply.ModeAmbient}, post)
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
