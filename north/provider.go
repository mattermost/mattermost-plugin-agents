// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package north

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// Provider implements llm.LanguageModel on top of the Cohere North native
// chat API. The agent loop is delegated to North; how far depends on the
// Mattermost agent's tool configuration:
//
//   - Tools disabled (pure delegation): requests omit the tools field, so the
//     North agent's own hosted tools stay active server-side. The provider
//     never emits EventTypeToolCalls and the plugin-side tool runner never
//     iterates.
//   - Tools enabled (hybrid): the Mattermost tool catalog is forwarded as
//     client-executed function tools. North returns TOOL_CALL rounds that the
//     plugin's tool runner executes locally (approval flow included). Because
//     North rejects mixing hosted and function tools in one request
//     (CANNOT_MIX_CUSTOM_AND_MANAGED_TOOLS), the provider also injects a
//     bridge tool (north_agent_task) that reaches the agent's hosted tools
//     through a nested, hosted-tools-only North call.
//
// The North agent to delegate to is carried in the model fields
// (BotConfig.Model, falling back to ServiceConfig.DefaultModel); an empty
// value uses the North instance's default agent.
type Provider struct {
	client           *Client
	defaultAgentID   string
	inputTokenLimit  int
	outputTokenLimit int

	// hostedToolsMu guards hostedToolsCache, which caches the hosted-tool
	// names of North agents (successful lookups only).
	hostedToolsMu    sync.Mutex
	hostedToolsCache map[string][]string
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
		hostedToolsCache: make(map[string][]string),
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
	if !cfg.ToolsDisabled {
		chatRequest.Tools = functionToolsFromStore(toolStore(request))
	}
	return chatRequest
}

// toolStore extracts the request's tool store, which may be nil.
func toolStore(request llm.CompletionRequest) *llm.ToolStore {
	if request.Context == nil {
		return nil
	}
	return request.Context.Tools
}

// functionToolsFromStore converts the Mattermost tool catalog into North
// function tool definitions. An empty catalog returns nil so the tools field
// is omitted entirely, keeping the North agent's hosted tools active.
func functionToolsFromStore(store *llm.ToolStore) []ChatTool {
	if store == nil {
		return nil
	}
	tools := store.GetTools()
	if len(tools) == 0 {
		return nil
	}
	chatTools := make([]ChatTool, 0, len(tools))
	for _, tool := range tools {
		chatTools = append(chatTools, ChatTool{
			Type: "function",
			Function: &FunctionToolDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schemaToParameters(tool.Schema),
			},
		})
	}
	return chatTools
}

// schemaToParameters converts a tool schema (typically *jsonschema.Schema)
// into the plain JSON object North expects for function parameters. North
// validates function schemas strictly and rejects object schemas without a
// properties key (INVALID_FUNCTION_TOOL: "Schema is missing required keys"),
// which no-parameter tools commonly omit.
func schemaToParameters(schema any) map[string]any {
	minimal := func() map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if schema == nil {
		return minimal()
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return minimal()
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil || len(params) == 0 {
		return minimal()
	}
	if schemaType, _ := params["type"].(string); schemaType == "object" || schemaType == "" {
		if _, ok := params["properties"]; !ok {
			params["properties"] = map[string]any{}
		}
	}
	return params
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

// mapPosts converts plugin posts into North chat messages. Text and tool
// transcripts are forwarded; file/vision content is not part of the POC
// surface.
func mapPosts(posts []llm.Post) []ChatMessage {
	messages := make([]ChatMessage, 0, len(posts))
	for _, post := range posts {
		if post.Role == llm.PostRoleBot && len(post.ToolUse) > 0 {
			messages = append(messages, mapToolUsePost(post)...)
			continue
		}
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

// mapToolUsePost converts a bot post carrying tool calls into an assistant
// message with tool_calls followed by one tool-result message per call.
func mapToolUsePost(post llm.Post) []ChatMessage {
	assistant := ChatMessage{Role: "assistant", Content: post.Message}
	results := make([]ChatMessage, 0, len(post.ToolUse))
	for _, toolCall := range post.ToolUse {
		arguments := strings.TrimSpace(string(toolCall.Arguments))
		if arguments == "" {
			arguments = "{}"
		}
		assistant.ToolCalls = append(assistant.ToolCalls, MessageToolCall{
			ToolCallID: toolCall.ID,
			Type:       "function",
			Function:   MessageToolFunction{Name: toolCall.Name, Arguments: arguments},
		})
		result := toolCall.Result
		if result == "" {
			result = "(no output)"
		}
		results = append(results, ChatMessage{Role: "tool", ToolCallID: toolCall.ID, Content: result})
	}
	return append([]ChatMessage{assistant}, results...)
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

// BridgeToolName is the synthetic function tool that gives hybrid agents
// access to the North agent's hosted tools. North rejects requests mixing
// hosted and function tools, so hosted capabilities are reached through a
// nested, hosted-tools-only chat call instead.
const BridgeToolName = "north_agent_task"

type northAgentTaskArgs struct {
	Task string `json:"task" jsonschema:"A fully self-contained task for the North agent, including all context it needs."`
}

// maybeInjectBridgeTool adds the north_agent_task tool to the request's tool
// store when the request runs in hybrid mode (Mattermost tools present) and
// the target North agent actually has hosted tools. Failures to look up the
// agent silently skip injection; the next round retries.
func (p *Provider) maybeInjectBridgeTool(ctx context.Context, request llm.CompletionRequest, cfg llm.LanguageModelConfig) {
	if cfg.ToolsDisabled || cfg.Model == "" {
		return
	}
	store := toolStore(request)
	if store == nil || len(store.GetTools()) == 0 {
		return
	}
	hostedNames := p.hostedToolNames(ctx, cfg.Model)
	if len(hostedNames) == 0 {
		return
	}
	store.AddTools([]llm.Tool{p.bridgeTool(cfg.Model, hostedNames)})
}

// hostedToolNames returns the hosted-tool names of a North agent, caching
// successful lookups per agent ID.
func (p *Provider) hostedToolNames(ctx context.Context, agentID string) []string {
	p.hostedToolsMu.Lock()
	defer p.hostedToolsMu.Unlock()
	if names, ok := p.hostedToolsCache[agentID]; ok {
		return names
	}
	agent, err := p.client.GetAgent(ctx, agentID)
	if err != nil {
		// Not cached: transient failures should not permanently disable the
		// bridge for this provider instance.
		return nil
	}
	names := make([]string, 0, len(agent.Tools))
	for _, tool := range agent.Tools {
		if tool.NorthTool != nil && tool.NorthTool.Name != "" {
			names = append(names, tool.NorthTool.Name)
		}
	}
	p.hostedToolsCache[agentID] = names
	return names
}

// bridgeTool builds the north_agent_task tool bound to a North agent.
func (p *Provider) bridgeTool(agentID string, hostedToolNames []string) llm.Tool {
	return llm.Tool{
		Name: BridgeToolName,
		Description: fmt.Sprintf(
			"Delegate a task to the Cohere North agent, which executes it server-side using its own tools (%s). "+
				"Use this for anything that needs live web information, fetching/scraping a URL, or running code/data analysis. "+
				"The task must be fully self-contained: include all relevant context, since the North agent cannot see this conversation.",
			strings.Join(hostedToolNames, ", "),
		),
		Schema: llm.NewJSONSchemaFromStruct[northAgentTaskArgs](),
		Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			var args northAgentTaskArgs
			if err := argsGetter(&args); err != nil {
				return "", fmt.Errorf("failed to get north_agent_task arguments: %w", err)
			}
			if strings.TrimSpace(args.Task) == "" {
				return "", errors.New("task must not be empty")
			}
			return p.runAgentTask(ctx, agentID, args.Task)
		},
	}
}

// runAgentTask performs the nested hosted-tools-only North call backing the
// bridge tool and formats the result (answer text plus source URLs).
func (p *Provider) runAgentTask(ctx context.Context, agentID, task string) (string, error) {
	response, err := p.client.Chat(ctx, ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: task}},
		Stateless: true,
		Agent:     &AgentRef{ID: agentID},
		// Tools omitted on purpose: the agent's hosted tools run server-side.
	})
	if err != nil {
		return "", err
	}
	if response.Error != nil {
		return "", response.Error
	}

	var text strings.Builder
	var sources []string
	seenSources := make(map[string]bool)
	for _, message := range response.Messages {
		if message.Role != "" && message.Role != "assistant" {
			continue
		}
		for _, item := range message.ContentItems() {
			if item.Type == "text" {
				text.WriteString(item.Text)
			}
		}
		for _, citation := range message.Citations {
			url, title := citationSourceURL(citation)
			if url == "" || seenSources[url] {
				continue
			}
			seenSources[url] = true
			if title != "" {
				sources = append(sources, fmt.Sprintf("- %s: %s", title, url))
			} else {
				sources = append(sources, "- "+url)
			}
		}
	}
	if text.Len() == 0 {
		return "", errors.New("north agent returned no text")
	}
	if len(sources) > 0 {
		text.WriteString("\n\nSources:\n")
		text.WriteString(strings.Join(sources, "\n"))
	}
	return text.String(), nil
}

// ChatCompletion sends the conversation to North and streams the delegated
// agent's response back as plugin stream events.
func (p *Provider) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	cfg := p.languageModelConfig(opts)

	ctx, span := p.startSpan(ctx, request.Operation, cfg, true)

	p.maybeInjectBridgeTool(ctx, request, cfg)
	chatRequest := p.buildChatRequest(request, cfg)

	events, err := p.client.ChatStream(ctx, chatRequest)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}

	// Offered function tool names: calls to these surface as plugin tool
	// calls; anything else is North-side activity and is only narrated.
	offeredTools := make(map[string]bool, len(chatRequest.Tools))
	for _, tool := range chatRequest.Tools {
		if tool.Function != nil {
			offeredTools[tool.Function.Name] = true
		}
	}

	output := make(chan llm.TextStreamEvent)
	go func() {
		defer close(output)
		defer span.End()
		translateStream(events, output, span, offeredTools)
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

// pendingToolCall accumulates one streamed function tool call by index.
type pendingToolCall struct {
	id        string
	name      string
	arguments strings.Builder
	offered   bool // name is one of the function tools we sent
	narrated  bool // reasoning line already emitted for non-offered calls
}

// translateStream converts North SSE events into plugin stream events.
//
// Mapping:
//   - text content deltas        → EventTypeText
//   - thinking content deltas    → EventTypeReasoning
//   - tool-plan deltas           → EventTypeReasoning
//   - tool-call-* for tools we offered → accumulated, emitted as
//     EventTypeToolCalls at stream end (the plugin tool runner executes them)
//   - tool-call-* for anything else → EventTypeReasoning progress line (the
//     call executes inside North; the plugin only narrates it)
//   - citation-start             → collected, emitted as EventTypeAnnotations
//   - usage on message/stream end→ EventTypeUsage
//   - stream-end                 → EventTypeEnd (or EventTypeError)
//   - debug                      → dropped (contains the raw prompt)
func translateStream(events <-chan StreamEvent, output chan<- llm.TextStreamEvent, span trace.Span, offeredTools map[string]bool) {
	var fullText strings.Builder
	var reasoning strings.Builder
	var citations []Citation
	var usage llm.TokenUsage
	pendingCalls := make(map[int]*pendingToolCall)
	var callOrder []int
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
	// offeredToolCalls returns the accumulated calls to tools we offered, in
	// stream order, as plugin tool calls.
	offeredToolCalls := func() []llm.ToolCall {
		var toolCalls []llm.ToolCall
		for _, index := range callOrder {
			call := pendingCalls[index]
			if call == nil || !call.offered {
				continue
			}
			arguments := strings.TrimSpace(call.arguments.String())
			if arguments == "" {
				arguments = "{}"
			}
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:        call.id,
				Name:      call.name,
				Arguments: json.RawMessage(arguments),
				Status:    llm.ToolCallStatusPending,
			})
		}
		return toolCalls
	}
	finish := func() {
		flushReasoning()
		toolCalls := offeredToolCalls()
		if len(toolCalls) == 0 {
			// Annotations only make sense on a final text answer.
			if annotations := annotationsFromCitations(fullText.String(), citations); len(annotations) > 0 {
				output <- llm.TextStreamEvent{Type: llm.EventTypeAnnotations, Value: annotations}
			}
		}
		if sawUsage {
			output <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: usage}
		}
		if len(toolCalls) > 0 {
			output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
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
		case "tool-call-start", "tool-call-delta":
			if event.Delta == nil || event.Delta.Message == nil || event.Delta.Message.ToolCalls == nil {
				continue
			}
			index := 0
			if event.Index != nil {
				index = *event.Index
			}
			call := pendingCalls[index]
			if call == nil {
				call = &pendingToolCall{}
				pendingCalls[index] = call
				callOrder = append(callOrder, index)
			}
			delta := event.Delta.Message.ToolCalls
			if delta.ToolCallID != "" {
				call.id = delta.ToolCallID
			}
			if delta.Function != nil {
				if delta.Function.Name != "" {
					call.name = delta.Function.Name
					call.offered = offeredTools[call.name]
				}
				call.arguments.WriteString(delta.Function.Arguments)
			}
			if call.name == "" && delta.DisplayName != "" {
				call.name = delta.DisplayName
				call.offered = offeredTools[call.name]
			}
			// North-side tool activity (anything we didn't offer) is narrated
			// once; offered tools get the real tool-call UI instead.
			if !call.offered && !call.narrated && call.name != "" {
				call.narrated = true
				displayName := delta.DisplayName
				if displayName == "" {
					displayName = call.name
				}
				emitReasoning(fmt.Sprintf("\nRunning North tool: %s\n", displayName))
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
		// tool-call-end, citation-end, debug) carries nothing the plugin
		// renders. debug events in particular contain the raw prompt and
		// must never be forwarded.
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
