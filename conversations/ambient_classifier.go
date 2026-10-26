// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	ambientClassifierMaxPosts       = 20
	ambientClassifierMaxThreadRunes = 16000
	ambientClassifierMaxTokens      = 128
)

var ambientClassifierTimeout = 8 * time.Second

// errAmbientDeclined marks the classifier deciding not to reply. Unlike other
// auto-reply no-ops it is a deliberate answer, so it must not trigger the
// @mention reminder.
var errAmbientDeclined = fmt.Errorf("ambient classifier declined: %w", ErrNoResponse)

// ambientClassifierJSON is the structured output schema.
type ambientClassifierJSON struct {
	ShouldReply bool `json:"should_reply"`
}

// ambientThreadPosts returns the trigger's thread oldest first, always
// including the trigger. A top-level post is its own thread, so it needs no
// fetch.
func (c *Conversations) ambientThreadPosts(trigger *model.Post) ([]*model.Post, error) {
	if trigger.RootId == "" {
		return []*model.Post{trigger}, nil
	}
	list, err := c.mmClient.GetPostThread(trigger.RootId)
	if err != nil {
		return nil, err
	}
	posts := slices.Collect(maps.Values(list.Posts))
	if _, ok := list.Posts[trigger.Id]; !ok {
		posts = append(posts, trigger)
	}
	slices.SortFunc(posts, func(a, b *model.Post) int {
		return cmp.Or(cmp.Compare(a.CreateAt, b.CreateAt), strings.Compare(a.Id, b.Id))
	})
	return posts, nil
}

// threadHasReplyFrom reports whether userID authored a reply in the thread.
// The root post is excluded so a root posted by the agent itself does not
// count as the agent having been brought in.
func threadHasReplyFrom(thread []*model.Post, userID string) bool {
	return slices.ContainsFunc(thread, func(p *model.Post) bool {
		return p.RootId != "" && p.UserId == userID
	})
}

// classifyAmbientReply returns nil only when the classifier says the agent
// should reply. Every other outcome wraps ErrNoResponse (fail-closed).
//
// TODO(ambient-poc): other native provider tools (web fetch, code interpreter)
// stay attached and rely on tool_choice "none". Structured-output capability
// detection, dedupe, concurrency control, rate/cost
// caps, and deleted-post recheck are deferred.
func (c *Conversations) classifyAmbientReply(ctx context.Context, bot *bots.Bot, setting *autoreply.Setting, trigger *model.Post, postingUser *model.User, channel *model.Channel, thread []*model.Post) error {
	ctx, span := telemetry.Tracer().Start(ctx, "ambient classifier", trace.WithAttributes(
		telemetry.AgentID.String(bot.GetMMBot().UserId),
		telemetry.ChannelID.String(channel.Id),
		telemetry.PostID.String(trigger.Id),
	))
	defer span.End()
	fail := func(status string, cause error) error {
		span.RecordError(cause)
		span.SetStatus(codes.Error, status)
		return fmt.Errorf("ambient classifier %s: %v: %w", status, cause, ErrNoResponse)
	}
	// Failures an admin must be able to diagnose are logged at warn; every
	// failure still declines.
	warnFail := func(status string, cause error) error {
		c.mmClient.LogWarn("Ambient auto-reply classifier failed: "+status, "post_id", trigger.Id, "channel_id", channel.Id, "error", cause.Error())
		return fail(status, cause)
	}

	classifyCtx, cancel := context.WithTimeout(ctx, ambientClassifierTimeout)
	defer cancel()

	recent := model.NewPostList()
	for _, p := range newestPostsIncluding(thread, trigger, ambientClassifierMaxPosts) {
		recent.AddPost(p)
		recent.AddOrder(p.Id)
	}
	threadData, err := mmapi.GetMetadataForPosts(c.mmClient, recent)
	if err != nil {
		return warnFail("failed to load thread authors", err)
	}

	// The posting user attributes the request so provider fallbacks are limited
	// to services that user may use; thread content must not reach others.
	llmCtx := c.contextBuilder.BuildLLMContextUserRequest(bot, postingUser, channel,
		c.contextBuilder.WithLLMContextParameters(map[string]any{
			"Instructions": setting.Instructions,
			"ThreadScoped": setting.Mode == autoreply.ModeAmbientThreads,
		}),
	)

	prompt, err := c.prompts.Format(prompts.PromptAmbientClassifierSystem, llmCtx)
	if err != nil {
		return warnFail("failed to format prompt", err)
	}

	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: prompt},
			{Role: llm.PostRoleUser, Message: wrapAmbientThreadUserMessage(formatAmbientThread(threadData))},
		},
		Context:   llmCtx,
		Operation: llm.OperationAmbientClassification,
	}

	opts := []llm.LanguageModelOption{
		llm.WithJSONOutput[ambientClassifierJSON](),
		llm.WithToolsDisabled(),
		// tool_choice "none" does not reach Gemini when no function tools are
		// declared, so native web search must be omitted outright.
		llm.WithSkipNativeWebSearch(),
		llm.WithReasoningDisabled(),
		llm.WithMaxGeneratedTokens(ambientClassifierMaxTokens),
	}

	raw, err := bot.LLM().ChatCompletionNoStream(classifyCtx, req, opts...)
	if err != nil {
		return warnFail("provider error", llm.SanitizeProviderError(err))
	}

	shouldReply, parseErr := parseAmbientClassifierOutput(raw)
	if parseErr != nil {
		return fail("malformed classifier output", parseErr)
	}
	span.SetAttributes(telemetry.AmbientShouldReply.Bool(shouldReply))
	if !shouldReply {
		return errAmbientDeclined
	}
	return nil
}

func wrapAmbientThreadUserMessage(formatted string) string {
	return "---- Thread Start ----\n" + formatted + "\n---- Thread End ----"
}

// newestPostsIncluding keeps the newest n posts of an oldest-first thread. If
// the trigger would fall out of that window it replaces the oldest kept post.
func newestPostsIncluding(thread []*model.Post, trigger *model.Post, n int) []*model.Post {
	if len(thread) <= n {
		return thread
	}
	newest := thread[len(thread)-n:]
	if slices.ContainsFunc(newest, func(p *model.Post) bool { return p.Id == trigger.Id }) {
		return newest
	}
	return append([]*model.Post{trigger}, newest[1:]...)
}

func formatAmbientThread(data *mmapi.ThreadData) string {
	return trimRunesKeepNewest(format.ThreadData(data), ambientClassifierMaxThreadRunes)
}

func trimRunesKeepNewest(s string, maxRunes int) string {
	i := len(s)
	for n := 0; n < maxRunes && i > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return s[i:]
}

// parseAmbientClassifierOutput distinguishes a missing should_reply from false
// so a malformed answer fails closed instead of reading as a decline.
func parseAmbientClassifierOutput(raw string) (bool, error) {
	var payload struct {
		ShouldReply *bool `json:"should_reply"`
	}
	if err := json.Unmarshal([]byte(llm.StripMarkdownCodeFencing(raw)), &payload); err != nil {
		return false, errors.New("malformed JSON")
	}
	if payload.ShouldReply == nil {
		return false, errors.New("missing should_reply")
	}
	return *payload.ShouldReply, nil
}
