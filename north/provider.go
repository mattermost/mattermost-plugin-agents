// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package north

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// Provider implements llm.LanguageModel on top of the Cohere North native
// chat API. The agent loop is fully delegated to North: any tools attached to
// the North agent run server-side inside North, and the provider never emits
// EventTypeToolCalls, so the plugin-side tool runner never iterates.
//
// The North agent to delegate to is carried in the model fields
// (BotConfig.Model, falling back to ServiceConfig.DefaultModel); an empty
// value uses the North instance's default agent.
type Provider struct {
	client           *Client
	defaultAgentID   string
	inputTokenLimit  int
	outputTokenLimit int
}

// New creates a North-backed language model from the service and bot
// configuration.
func New(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig) *Provider {
	agentID := botConfig.Model
	if agentID == "" {
		agentID = serviceConfig.DefaultModel
	}
	return &Provider{
		client: NewClient(
			serviceConfig.APIURL,
			serviceConfig.APIKey,
			time.Duration(serviceConfig.StreamingTimeoutSeconds)*time.Second,
		),
		defaultAgentID:   agentID,
		inputTokenLimit:  serviceConfig.InputTokenLimit,
		outputTokenLimit: serviceConfig.OutputTokenLimit,
	}
}

// buildChatRequest maps a plugin completion request onto a North native chat
// request. Requests are stateless with the full mapped history, matching how
// the plugin reconstructs the thread every turn.
func (p *Provider) buildChatRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) ChatRequest {
	chatRequest := ChatRequest{
		Messages:  mapPosts(request.Posts),
		Stateless: true,
		MaxTokens: cfg.MaxGeneratedTokens,
	}
	if cfg.Model != "" {
		chatRequest.Agent = &AgentRef{ID: cfg.Model}
	}
	if cfg.ReasoningDisabled {
		chatRequest.Thinking = &ThinkingOptions{Type: "disabled"}
	}
	return chatRequest
}

func (p *Provider) languageModelConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := llm.LanguageModelConfig{
		Model:              p.defaultAgentID,
		MaxGeneratedTokens: p.outputTokenLimit,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// mapPosts converts plugin posts into North chat messages. Only text is
// forwarded: file/vision content and tool transcripts are not part of the
// delegated POC surface.
func mapPosts(posts []llm.Post) []ChatMessage {
	messages := make([]ChatMessage, 0, len(posts))
	for _, post := range posts {
		if strings.TrimSpace(post.Message) == "" {
			continue
		}
		var role string
		switch post.Role {
		case llm.PostRoleSystem:
			role = "system"
		case llm.PostRoleBot:
			role = "assistant"
		default:
			role = "user"
		}
		messages = append(messages, ChatMessage{Role: role, Content: post.Message})
	}
	return messages
}

func (p *Provider) startSpan(ctx context.Context, operation string, cfg llm.LanguageModelConfig, streaming bool) (context.Context, trace.Span) {
	return telemetry.Tracer().Start(ctx, "north chat",
		trace.WithAttributes(
			telemetry.LLMProvider.String(llm.ServiceTypeNorth),
			telemetry.LLMModel.String(cfg.Model),
			telemetry.LLMOperation.String(operation),
			telemetry.LLMStreaming.Bool(streaming),
		),
	)
}

// ChatCompletion sends the conversation to North and streams the delegated
// agent's response back as plugin stream events.
func (p *Provider) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	cfg := p.languageModelConfig(opts)

	ctx, span := p.startSpan(ctx, request.Operation, cfg, true)

	events, err := p.client.ChatStream(ctx, p.buildChatRequest(request, cfg))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}

	output := make(chan llm.TextStreamEvent)
	go func() {
		defer close(output)
		defer span.End()
		translateStream(events, output, span)
	}()

	return &llm.TextStreamResult{Stream: output}, nil
}

// ChatCompletionNoStream sends the conversation to North and returns the
// delegated agent's final text.
func (p *Provider) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	cfg := p.languageModelConfig(opts)

	ctx, span := p.startSpan(ctx, request.Operation, cfg, false)
	defer span.End()

	response, err := p.client.Chat(ctx, p.buildChatRequest(request, cfg))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	if response.Error != nil {
		span.RecordError(response.Error)
		span.SetStatus(codes.Error, response.Error.Message)
		return "", response.Error
	}

	var text strings.Builder
	for _, message := range response.Messages {
		if message.Role != "" && message.Role != "assistant" {
			continue
		}
		for _, item := range message.ContentItems() {
			if item.Type == "text" {
				text.WriteString(item.Text)
			}
		}
	}
	return text.String(), nil
}

// CountTokens is unsupported; callers fall back to llm.EstimateTokens.
func (p *Provider) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}

func (p *Provider) InputTokenLimit() int {
	return p.inputTokenLimit
}

func (p *Provider) OutputTokenLimit() int {
	return p.outputTokenLimit
}

// translateStream converts North SSE events into plugin stream events.
//
// Mapping:
//   - text content deltas        → EventTypeText
//   - thinking content deltas    → EventTypeReasoning
//   - tool-plan deltas           → EventTypeReasoning
//   - tool-call-start            → EventTypeReasoning progress line (the call
//     executes inside North; the plugin only narrates it)
//   - citation-start             → collected, emitted as EventTypeAnnotations
//   - usage on message/stream end→ EventTypeUsage
//   - stream-end                 → EventTypeEnd (or EventTypeError)
//   - debug                      → dropped (contains the raw prompt)
func translateStream(events <-chan StreamEvent, output chan<- llm.TextStreamEvent, span trace.Span) {
	var fullText strings.Builder
	var reasoning strings.Builder
	var citations []Citation
	var usage llm.TokenUsage
	sawUsage := false
	ended := false

	// flushReasoning closes an open reasoning block before text resumes.
	flushReasoning := func() {
		if reasoning.Len() == 0 {
			return
		}
		output <- llm.TextStreamEvent{Type: llm.EventTypeReasoningEnd, Value: llm.ReasoningData{Text: reasoning.String()}}
		reasoning.Reset()
	}
	emitReasoning := func(text string) {
		if text == "" {
			return
		}
		reasoning.WriteString(text)
		output <- llm.TextStreamEvent{Type: llm.EventTypeReasoning, Value: text}
	}
	recordUsage := func(u *Usage) {
		if u == nil || u.Tokens == nil {
			return
		}
		usage = llm.TokenUsage{
			InputTokens:      u.Tokens.InputTokens,
			OutputTokens:     u.Tokens.OutputTokens,
			CachedReadTokens: u.CachedTokens,
		}
		sawUsage = true
	}
	fail := func(err error) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		output <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: err}
		ended = true
	}
	finish := func() {
		flushReasoning()
		if annotations := annotationsFromCitations(fullText.String(), citations); len(annotations) > 0 {
			output <- llm.TextStreamEvent{Type: llm.EventTypeAnnotations, Value: annotations}
		}
		if sawUsage {
			output <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: usage}
		}
		output <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		ended = true
	}

	for event := range events {
		if ended {
			continue
		}
		switch event.Type {
		case "content-delta", "content-start":
			if event.Delta == nil || event.Delta.Message == nil {
				continue
			}
			item, ok := event.Delta.Message.DeltaContent()
			if !ok {
				continue
			}
			switch item.Type {
			case "thinking":
				text := item.Thinking
				if text == "" {
					text = item.Text
				}
				emitReasoning(text)
			case "text":
				flushReasoning()
				if item.Text != "" {
					fullText.WriteString(item.Text)
					output <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: item.Text}
				}
			}
		case "tool-plan-delta":
			if event.Delta != nil && event.Delta.Message != nil {
				emitReasoning(event.Delta.Message.ToolPlan)
			}
		case "tool-call-start":
			if event.Delta == nil || event.Delta.Message == nil || event.Delta.Message.ToolCalls == nil {
				continue
			}
			toolCall := event.Delta.Message.ToolCalls
			name := toolCall.DisplayName
			if name == "" && toolCall.Function != nil {
				name = toolCall.Function.Name
			}
			if name != "" {
				emitReasoning(fmt.Sprintf("\nRunning North tool: %s\n", name))
			}
		case "citation-start":
			if event.Delta != nil && event.Delta.Message != nil && event.Delta.Message.Citations != nil {
				citations = append(citations, *event.Delta.Message.Citations)
			}
		case "message-end":
			if event.Delta != nil {
				recordUsage(event.Delta.Usage)
			}
		case "stream-end":
			if event.Delta != nil {
				recordUsage(event.Delta.Usage)
				if event.Delta.Error != nil {
					fail(event.Delta.Error)
					continue
				}
			}
			finish()
		case "error":
			if event.Delta != nil && event.Delta.Error != nil {
				fail(event.Delta.Error)
			} else {
				fail(errors.New("north: stream failed"))
			}
		}
		// Everything else (stream-start, message-start, content-end,
		// tool-call-delta/end, citation-end, debug) carries nothing the
		// plugin renders. debug events in particular contain the raw
		// prompt and must never be forwarded.
	}

	if !ended {
		// The stream closed without a terminal event (idle timeout or
		// severed connection).
		fail(errors.New("north: stream ended unexpectedly"))
	}
}

// annotationsFromCitations converts North citations into plugin URL-citation
// annotations. Only citations whose cited text can be located in the final
// message and that carry a source URL are kept; indices are converted to the
// JS UTF-16 code units the webapp expects.
func annotationsFromCitations(fullText string, citations []Citation) []llm.Annotation {
	var annotations []llm.Annotation
	for _, citation := range citations {
		url, title := citationSourceURL(citation)
		if url == "" || citation.Text == "" {
			continue
		}
		byteStart := strings.Index(fullText, citation.Text)
		if byteStart < 0 {
			continue
		}
		start := llm.UTF16CodeUnitCount(fullText[:byteStart])
		annotations = append(annotations, llm.Annotation{
			Type:       llm.AnnotationTypeURLCitation,
			StartIndex: start,
			EndIndex:   start + llm.UTF16CodeUnitCount(citation.Text),
			URL:        url,
			Title:      title,
			CitedText:  citation.Text,
			Index:      len(annotations) + 1,
		})
	}
	return annotations
}

// citationSourceURL extracts a URL and title from the first document source
// that carries them.
func citationSourceURL(citation Citation) (url, title string) {
	for _, source := range citation.Sources {
		doc := source.Document
		if doc == nil {
			continue
		}
		u, _ := doc["url"].(string)
		if u == "" {
			continue
		}
		t, _ := doc["title"].(string)
		return u, t
	}
	return "", ""
}
