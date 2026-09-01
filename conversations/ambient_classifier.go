// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"fmt"
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
)

const (
	ambientClassifierMaxPosts       = 20
	ambientClassifierMaxThreadRunes = 16000
	ambientClassifierMaxTokens      = 128
)

var ambientClassifierTimeout = 8 * time.Second

// ambientClassifierJSON is the structured output schema. Parsing still
// distinguishes a missing should_reply field from false.
type ambientClassifierJSON struct {
	ShouldReply bool `json:"should_reply"`
}

// classifyAmbientReply returns nil only when the classifier says the agent
// should reply. Every other outcome wraps ErrNoResponse (fail-closed).
//
// TODO(ambient-poc): WithToolsDisabled does not fully disable native provider
// tools; this POC assumes no native tools are enabled.
// TODO(ambient-poc): fallback chains may cross services; this POC assumes no
// provider fallback is in play.
// TODO(ambient-poc): provider structured-output capability detection is deferred.
// TODO(ambient-poc): analysis_model discovery/validation is deferred.
// TODO(ambient-poc): dedupe, concurrency control, rate/cost caps, and
// deleted-post recheck are deferred.
func (c *Conversations) classifyAmbientReply(ctx context.Context, bot *bots.Bot, setting *autoreply.Setting, post *model.Post) error {
	ctx, span := telemetry.Tracer().Start(ctx, "ambient classifier")
	defer span.End()

	if bot == nil || bot.LLM() == nil {
		return fmt.Errorf("ambient classifier has no language model: %w", ErrNoResponse)
	}
	if c.prompts == nil {
		return fmt.Errorf("ambient classifier has no prompts: %w", ErrNoResponse)
	}

	classifyCtx, cancel := context.WithTimeout(ctx, ambientClassifierTimeout)
	defer cancel()

	threadID := post.Id
	if post.RootId != "" {
		threadID = post.RootId
	}
	threadData, err := mmapi.GetThreadData(c.mmClient, threadID)
	if err != nil || threadData == nil {
		return fmt.Errorf("ambient classifier failed to load thread: %w", ErrNoResponse)
	}

	formatted := boundAmbientThread(threadData, post)
	llmCtx := llm.NewContext()
	llmCtx.Parameters = map[string]any{
		"Instructions": setting.Instructions,
	}

	prompt, err := c.prompts.Format(prompts.PromptAmbientClassifierSystem, llmCtx)
	if err != nil {
		return fmt.Errorf("ambient classifier failed to format prompt: %w", ErrNoResponse)
	}

	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: prompt},
			{Role: llm.PostRoleUser, Message: wrapAmbientThreadUserMessage(formatted)},
		},
		Context:   llmCtx,
		Operation: llm.OperationAmbientClassification,
	}

	opts := []llm.LanguageModelOption{
		llm.WithJSONOutput[ambientClassifierJSON](),
		llm.WithToolsDisabled(),
		llm.WithReasoningDisabled(),
		llm.WithMaxGeneratedTokens(ambientClassifierMaxTokens),
	}
	if setting.AnalysisModel != "" {
		opts = append(opts, llm.WithModel(setting.AnalysisModel))
	}

	raw, err := bot.LLM().ChatCompletionNoStream(classifyCtx, req, opts...)
	if err != nil {
		return fmt.Errorf("ambient classifier provider error: %w", ErrNoResponse)
	}

	shouldReply, parseErr := parseAmbientClassifierOutput(raw)
	if parseErr != nil {
		return fmt.Errorf("ambient classifier output: %v: %w", parseErr, ErrNoResponse)
	}
	if !shouldReply {
		return fmt.Errorf("ambient classifier declined: %w", ErrNoResponse)
	}
	return nil
}

func wrapAmbientThreadUserMessage(formatted string) string {
	return "---- Thread Start ----\n" + formatted + "\n---- Thread End ----"
}

func boundAmbientThread(data *mmapi.ThreadData, trigger *model.Post) string {
	posts := data.Posts
	if trigger != nil {
		found := false
		for _, p := range posts {
			if p != nil && p.Id == trigger.Id {
				found = true
				break
			}
		}
		if !found {
			copied := make([]*model.Post, len(posts)+1)
			copy(copied, posts)
			copied[len(posts)] = trigger
			posts = copied
		}
	}

	if len(posts) > ambientClassifierMaxPosts {
		newest := posts[len(posts)-ambientClassifierMaxPosts:]
		if trigger != nil {
			hasTrigger := false
			for _, p := range newest {
				if p != nil && p.Id == trigger.Id {
					hasTrigger = true
					break
				}
			}
			if !hasTrigger {
				trimmed := make([]*model.Post, ambientClassifierMaxPosts)
				trimmed[0] = trigger
				copy(trimmed[1:], newest[1:])
				newest = trimmed
			}
		}
		posts = newest
	}

	formatted := format.ThreadData(&mmapi.ThreadData{Posts: posts, UsersByID: data.UsersByID})
	return trimRunesKeepNewest(formatted, ambientClassifierMaxThreadRunes)
}

func trimRunesKeepNewest(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[len(runes)-maxRunes:])
}

func parseAmbientClassifierOutput(raw string) (bool, error) {
	raw = llm.StripMarkdownCodeFencing(raw)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return false, fmt.Errorf("malformed JSON")
	}
	field, ok := payload["should_reply"]
	if !ok {
		return false, fmt.Errorf("missing should_reply")
	}
	var shouldReply bool
	if err := json.Unmarshal(field, &shouldReply); err != nil {
		return false, fmt.Errorf("malformed should_reply")
	}
	return shouldReply, nil
}
