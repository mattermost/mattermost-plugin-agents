// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package bifrost provides a unified LLM interface using the Bifrost gateway library.
// This package wraps Bifrost to implement the llm.LanguageModel interface, allowing
// the plugin to use multiple LLM providers through a single, consistent API.
package bifrost

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"

	// Register standard image decoders for image.DecodeConfig.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	// Register the WebP decoder for image.DecodeConfig.
	_ "golang.org/x/image/webp"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

const (
	DefaultMaxTokens        = 8192
	MaxToolResolutionDepth  = 10
	DefaultStreamingTimeout = 5 * time.Minute
	// CountTokensTimeout caps the count-tokens preflight so a wedged provider
	// can't block the request handler.
	CountTokensTimeout = 30 * time.Second
	// FileDownloadTimeout caps a provider file download so a wedged provider
	// cannot hold a tool resolution open indefinitely.
	FileDownloadTimeout = 60 * time.Second
)

const (
	// anthropicBetaHeader and anthropicFilesAPIBeta mirror Bifrost's internal
	// (unexported) constants for the Anthropic beta opt-in header.
	anthropicBetaHeader   = "anthropic-beta"
	anthropicFilesAPIBeta = "files-api-2025-04-14"
)

// LLM implements the llm.LanguageModel interface using the Bifrost gateway.
type LLM struct {
	client           *bifrostcore.Bifrost
	provider         schemas.ModelProvider
	apiKey           string   // used only to redact configured secrets from provider error surfaces
	fallbackAPIKeys  []string // fallback provider keys, redacted from error surfaces alongside apiKey
	defaultModel     string
	inputTokenLimit  int
	outputTokenLimit int
	streamingTimeout time.Duration

	// Native tools and reasoning configuration
	enabledNativeTools []string
	reasoningEnabled   bool
	reasoningEffort    string
	thinkingBudget     int

	// UseResponsesAPI enables OpenAI Responses API for native tools support
	useResponsesAPI bool

	// fallbacks is attached to every outgoing request so Bifrost retries with
	// alternative providers when the primary fails.
	fallbacks []fallbackHop

	// account holds every service registered with the Bifrost client, keyed by
	// registration name (including custom fallback slots such as
	// "anthropic::svc2"). It is read-only once New returns.
	account *multiProviderAccount
}

// fallbackHop pairs a Bifrost fallback with the durable service ID it came
// from, so the chain can be prefix-filtered per requesting user.
type fallbackHop struct {
	fallback  schemas.Fallback
	serviceID string
}

// ProviderSettings holds the connection and credential fields needed to reach
// one provider. It is shared by the primary Config and every FallbackEntry in
// the chain.
type ProviderSettings struct {
	Provider           schemas.ModelProvider
	APIKey             string
	APIURL             string // Custom base URL (for Azure, OpenAI Compatible, etc.)
	OrgID              string
	Region             string // For AWS Bedrock and Vertex
	AWSAccessKeyID     string
	AWSSecretAccessKey string

	// Vertex AI (GCP). VertexAuthCredentials holds the service-account JSON;
	// empty falls back to ADC/IAM.
	VertexProjectID       string
	VertexProjectNumber   string
	VertexAuthCredentials string

	DefaultModel     string
	StreamingTimeout time.Duration

	// DisableStore maps onto schemas.OpenAIConfig.DisableStore so Bifrost
	// forces store=false on every outgoing OpenAI-family request for this
	// provider — including when it is reached as a fallback hop.
	DisableStore bool
}

// Config holds the configuration for creating a LLM instance.
type Config struct {
	ProviderSettings

	InputTokenLimit  int
	OutputTokenLimit int

	// Native tools and reasoning configuration
	EnabledNativeTools []string
	ReasoningEnabled   bool
	ReasoningEffort    string
	ThinkingBudget     int

	// UseResponsesAPI enables OpenAI Responses API for native tools support
	UseResponsesAPI bool

	// Fallbacks is the ordered list of providers Bifrost tries sequentially
	// when the primary provider fails.
	Fallbacks []FallbackEntry
}

// FallbackEntry holds the settings for a single fallback in the chain.
type FallbackEntry struct {
	ProviderSettings

	// ID is the source service ID, used to mint a unique custom-provider name
	// when this fallback shares a base provider type with another service.
	ID string
	// OutputTokenLimit is the service's configured output token limit; zero
	// means unset.
	OutputTokenLimit int
	// IsKeyLess marks a fallback that authenticates without an API key (e.g. a
	// local Ollama server).
	IsKeyLess bool
	// ChatOnly marks an OpenAI-base fallback whose endpoint lacks the Responses
	// API; it is registered chat-only so Bifrost downgrades Responses-API
	// requests to chat completions for it.
	ChatOnly bool
}

func readFileData(file llm.File) ([]byte, error) {
	if len(file.Data) > 0 {
		return file.Data, nil
	}
	if file.Reader == nil {
		return nil, fmt.Errorf("file reader is nil")
	}
	return io.ReadAll(file.Reader)
}

// bufferFiles returns request with the bytes of every image file read into
// Data. Each fallback hop rebuilds its messages, and a Reader can be read only
// once. A file that cannot be read keeps neither Data nor Reader, which
// multimodalContent reports as unreadable. The caller's posts are not modified.
func bufferFiles(request llm.CompletionRequest) llm.CompletionRequest {
	request.Posts = slices.Clone(request.Posts)
	for i := range request.Posts {
		if len(request.Posts[i].Files) == 0 {
			continue
		}
		request.Posts[i].Files = slices.Clone(request.Posts[i].Files)
		for j := range request.Posts[i].Files {
			file := &request.Posts[i].Files[j]
			if len(file.Data) > 0 || file.Reader == nil || !llm.IsSupportedImageMimeType(file.MimeType) {
				continue
			}
			data, err := io.ReadAll(file.Reader)
			switch {
			case err != nil:
				file.Reader = nil
			case len(data) > 0:
				file.Data, file.Reader = data, nil
			default:
				// An empty file reads as empty every time.
				file.Reader = bytes.NewReader(nil)
			}
		}
	}
	return request
}

// New creates a new LLM instance with the given configuration. It errors when
// a fallback cannot be registered, so a misconfigured fallback chain fails at
// setup instead of silently shrinking.
func New(cfg Config) (*LLM, error) {
	primaryEntry := &providerAccount{ProviderSettings: cfg.ProviderSettings}

	account := newMultiProviderAccount()
	account.addProvider(primaryEntry)

	// Bifrost registration names already taken. A fallback that shares a base
	// provider type with an earlier service must get its own custom-provider
	// slot, otherwise it silently inherits the earlier service's base URL and
	// key at fallback time.
	usedNames := map[schemas.ModelProvider]bool{primaryEntry.registeredName(): true}

	// Redact the primary and every fallback key from error/log surfaces, even
	// key formats the generic redaction patterns don't recognize.
	redactKeys := []string{cfg.APIKey}

	var fallbacks []fallbackHop
	for _, fb := range cfg.Fallbacks {
		if fb.APIKey != "" {
			redactKeys = append(redactKeys, fb.APIKey)
		}
		entry := &providerAccount{
			ProviderSettings: fb.ProviderSettings,
			keyless:          fb.IsKeyLess,
			outputTokenLimit: fb.OutputTokenLimit,
		}

		// A fallback needs its own custom-provider slot when it collides on an
		// already-registered name, is chat-only (the slot carries the downgrade
		// gate), or is keyless (the standard provider path would treat the empty
		// key as real credentials). Keyless is scoped to custom-capable base
		// types; providers like Vertex carry keyless auth (ADC) on their
		// standard config.
		name := fb.Provider
		if usedNames[name] || fb.ChatOnly || (fb.IsKeyLess && isCustomCapableProvider(fb.Provider)) {
			if !isCustomCapableProvider(fb.Provider) {
				return nil, fmt.Errorf("fallback service %q: provider %s is already registered and cannot host a second instance", fb.ID, fb.Provider)
			}
			name = customProviderName(fb.Provider, fb.ID)
			entry.name = name
			entry.chatOnly = fb.ChatOnly
		}
		if usedNames[name] {
			return nil, fmt.Errorf("fallback service %q resolves to already-registered provider %q", fb.ID, name)
		}

		account.addProvider(entry)
		usedNames[name] = true
		fallbacks = append(fallbacks, fallbackHop{
			fallback: schemas.Fallback{
				Provider: name,
				Model:    fb.DefaultModel,
			},
			serviceID: fb.ID,
		})
	}

	client, err := newBifrostClient(account, redactKeys...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client: %w", err)
	}

	streamingTimeout := cfg.StreamingTimeout
	if streamingTimeout == 0 {
		streamingTimeout = DefaultStreamingTimeout
	}

	return &LLM{
		client:             client,
		provider:           cfg.Provider,
		apiKey:             cfg.APIKey,
		fallbackAPIKeys:    redactKeys[1:],
		defaultModel:       cfg.DefaultModel,
		inputTokenLimit:    cfg.InputTokenLimit,
		outputTokenLimit:   cfg.OutputTokenLimit,
		streamingTimeout:   streamingTimeout,
		enabledNativeTools: cfg.EnabledNativeTools,
		reasoningEnabled:   cfg.ReasoningEnabled,
		reasoningEffort:    cfg.ReasoningEffort,
		thinkingBudget:     cfg.ThinkingBudget,
		useResponsesAPI:    cfg.UseResponsesAPI,
		fallbacks:          fallbacks,
		account:            account,
	}, nil
}

// Shutdown gracefully shuts down the Bifrost client.
func (b *LLM) Shutdown() {
	if b.client != nil {
		b.client.Shutdown()
	}
}

// redactionKeys returns every configured API key (primary plus fallbacks) so
// llm.SanitizeProviderError can strip any provider's credential from an error,
// not just the primary's.
func (b *LLM) redactionKeys() []string {
	keys := make([]string, 0, 1+len(b.fallbackAPIKeys))
	keys = append(keys, b.apiKey)
	keys = append(keys, b.fallbackAPIKeys...)
	return keys
}

// GetDefaultConfig returns the default language model configuration.
// MaxGeneratedTokens substitutes DefaultMaxTokens when unset because some
// providers (Anthropic) require it.
func (b *LLM) GetDefaultConfig() llm.LanguageModelConfig {
	maxGenerated := b.outputTokenLimit
	if maxGenerated == 0 {
		maxGenerated = DefaultMaxTokens
	}
	return llm.LanguageModelConfig{
		Model:              b.defaultModel,
		MaxGeneratedTokens: maxGenerated,
	}
}

// createConfig applies opts to the default config. MaxGeneratedTokens stays
// the requested value; each request builder and fallback hop clamps it to its
// own model's limit.
func (b *LLM) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := b.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// ChatCompletion performs a streaming chat completion request.
func (b *LLM) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	cfg := b.createConfig(opts)

	ctx, span := telemetry.Tracer().Start(ctx, "llm chat completion",
		telemetry.WithLLMAttributes(string(b.provider), cfg.Model, request.Operation, true),
	)
	if round, ok := telemetry.LLMRoundFromContext(ctx); ok {
		span.SetAttributes(telemetry.LLMRound.Int(round))
	}

	eventStream := make(chan llm.TextStreamEvent)
	providerStream := make(chan llm.TextStreamEvent)
	start := time.Now()

	go func() {
		defer close(providerStream)
		if b.shouldUseResponsesAPI(cfg) {
			b.streamResponses(ctx, request, cfg, providerStream)
		} else {
			b.streamChat(ctx, request, cfg, providerStream)
		}
	}()

	go func() {
		defer close(eventStream)
		defer span.End()
		firstToken := true
		for event := range providerStream {
			if firstToken && isModelOutputEvent(event) {
				firstToken = false
				span.SetAttributes(telemetry.LLMTimeToFirstTokenMs.Int64(time.Since(start).Milliseconds()))
			}
			eventStream <- event
		}
	}()

	return &llm.TextStreamResult{Stream: eventStream}, nil
}

// isModelOutputEvent reports whether event carries output generated by the
// model, as opposed to bookkeeping (usage, end, errors).
func isModelOutputEvent(event llm.TextStreamEvent) bool {
	switch event.Type {
	case llm.EventTypeText, llm.EventTypeReasoning, llm.EventTypeToolCalls, llm.EventTypeServerToolUse:
		return true
	}
	return false
}

// ChatCompletionNoStream performs a non-streaming chat completion request.
func (b *LLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := b.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// InputTokenLimit returns the configured maximum number of input tokens.
// Zero means "no client-side truncation" — the provider's own limit applies.
func (b *LLM) InputTokenLimit() int {
	return b.inputTokenLimit
}

// OutputTokenLimit returns the configured maximum number of output tokens.
// Zero means the request-building layer falls back to DefaultMaxTokens.
func (b *LLM) OutputTokenLimit() int {
	return b.outputTokenLimit
}

// bifrostUnsupportedOperationCode is the error Code Bifrost returns when a
// provider doesn't implement an operation (see providers/utils.NewUnsupportedOperationError).
// Bifrost exposes no capability query, so we detect this at call time rather than
// maintaining our own provider allowlist that would drift as Bifrost adds support.
const bifrostUnsupportedOperationCode = "unsupported_operation"

// CountTokens returns llm.ErrUnsupportedTokenCount when the provider lacks a
// count-tokens endpoint, signaling callers to fall back to llm.EstimateTokens.
func (b *LLM) CountTokens(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	cfg := b.createConfig(opts)
	request = bufferFiles(request)
	bifrostReq, err := b.convertToBifrostResponsesRequest(request, cfg)
	if err != nil {
		return 0, fmt.Errorf("failed to build count tokens request: %w", err)
	}
	// count_tokens shares the messages-endpoint schema but rejects some
	// params: OpenAI 400s on max_output_tokens, Anthropic 400s on native
	// server tools (web_search, file_search, code_interpreter). Reasoning
	// and response-format config don't change the input-token count, so we
	// keep only the function tool definitions — those DO count, and omitting
	// them undercounts every tools-enabled bot.
	if bifrostReq.Params != nil {
		bifrostReq.Params = &schemas.ResponsesParameters{
			Tools: functionToolsForCount(bifrostReq.Params.Tools),
		}
	}

	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(ctx, CountTokensTimeout)
	defer cancel()
	b.withHopRequest(bifrostCtx, request, cfg)
	resp, bifrostErr := b.client.CountTokensRequest(bifrostCtx, bifrostReq)
	if bifrostErr != nil {
		if bifrostErr.Error != nil && bifrostErr.Error.Code != nil && *bifrostErr.Error.Code == bifrostUnsupportedOperationCode {
			return 0, llm.ErrUnsupportedTokenCount
		}
		msg := "unknown error"
		if bifrostErr.Error != nil && bifrostErr.Error.Message != "" {
			msg = bifrostErr.Error.Message
		}
		return 0, llm.SanitizeProviderError(fmt.Errorf("bifrost count tokens error: %s", msg), b.redactionKeys()...)
	}
	if resp == nil {
		return 0, fmt.Errorf("bifrost count tokens returned nil response")
	}
	return resp.InputTokens, nil
}

// applyCompletionBetaHeaders opts into provider betas Bifrost does not set
// itself. Anthropic only reports sandbox file ids when the Files API beta is
// on the completion request; Bifrost adds that header only when the request
// already references a file, never because code execution is enabled.
func (b *LLM) applyCompletionBetaHeaders(bifrostCtx *schemas.BifrostContext) {
	if bifrostCtx == nil {
		return
	}
	// Any registered file-download route may end up serving the request —
	// Anthropic can be a fallback rather than the primary — so key the beta
	// opt-in off the routes, mirroring DownloadProviderFile. Only providers
	// needing the beta register a route; others ignore the extra header.
	if !b.hasFileDownloadRoute() || !b.isNativeToolEnabled(llm.NativeToolCodeInterpreter) {
		return
	}

	headers, _ := bifrostCtx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string)
	if headers == nil {
		headers = map[string][]string{}
	}
	if slices.Contains(headers[anthropicBetaHeader], anthropicFilesAPIBeta) {
		return
	}
	headers[anthropicBetaHeader] = append(headers[anthropicBetaHeader], anthropicFilesAPIBeta)
	bifrostCtx.SetValue(schemas.BifrostContextKeyExtraHeaders, headers)
}

// ProviderServices must be called on the concrete client before wrapping. A
// fallback can serve the turn that creates a file, so any registered download
// route exposes the downloader.
func (b *LLM) ProviderServices() *llm.ProviderServices {
	services := &llm.ProviderServices{}
	if b.hasFileDownloadRoute() {
		services.FileDownloader = b
	}
	return services
}

// hasFileDownloadRoute reports whether any registered route can serve
// captured files.
func (b *LLM) hasFileDownloadRoute() bool {
	if b.account == nil {
		return false
	}
	for _, entry := range b.account.entries {
		if supportsProviderFileDownloadProvider(entry.Provider) {
			return true
		}
	}
	return false
}

// DownloadProviderFile fetches a provider-side file. The captured reference
// selects the route so a fallback-created file uses that fallback's credentials.
// Filename comes from the metadata endpoint; the content response has none.
// A positive maxBytes rejects an oversized file from the metadata alone,
// before its content is transferred.
func (b *LLM) DownloadProviderFile(ctx context.Context, ref llm.ProviderFileReference, maxBytes int64) (llm.ProviderFile, error) {
	providerRoute := b.provider
	if ref.ProviderRoute != "" {
		providerRoute = schemas.ModelProvider(ref.ProviderRoute)
	}

	downloadCtx, span := telemetry.Tracer().Start(ctx, "download provider file",
		trace.WithAttributes(
			telemetry.LLMProvider.String(string(providerRoute)),
			telemetry.LLMOperation.String("file_download"),
			telemetry.LLMStreaming.Bool(false),
		),
	)
	defer span.End()

	fail := func(err error) (llm.ProviderFile, error) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return llm.ProviderFile{}, err
	}

	if ref.ID == "" {
		return fail(errors.New("file id is required"))
	}
	if entry := b.registration(providerRoute); entry == nil || !supportsProviderFileDownloadProvider(entry.Provider) {
		return fail(errors.New("provider file route is not available"))
	}

	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(downloadCtx, FileDownloadTimeout)
	defer cancel()

	meta, bifrostErr := b.client.FileRetrieveRequest(bifrostCtx, &schemas.BifrostFileRetrieveRequest{
		Provider: providerRoute,
		FileID:   ref.ID,
	})
	if bifrostErr != nil {
		err := providerError(nil, "bifrost file retrieve error", bifrostErr, b.redactionKeys()...)
		return fail(err)
	}
	if meta == nil {
		return fail(errors.New("bifrost file retrieve returned nil response"))
	}
	if maxBytes > 0 && meta.Bytes > maxBytes {
		return fail(fmt.Errorf("provider file is %d bytes, over the %d-byte limit", meta.Bytes, maxBytes))
	}

	resp, bifrostErr := b.client.FileContentRequest(bifrostCtx, &schemas.BifrostFileContentRequest{
		Provider: providerRoute,
		FileID:   ref.ID,
	})
	if bifrostErr != nil {
		err := providerError(nil, "bifrost file content error", bifrostErr, b.redactionKeys()...)
		return fail(err)
	}
	if resp == nil {
		return fail(errors.New("bifrost file content returned nil response"))
	}

	span.SetStatus(codes.Ok, "file download succeeded")
	return llm.ProviderFile{
		Name:        meta.Filename,
		ContentType: resp.ContentType,
		Content:     resp.Content,
	}, nil
}

// functionToolsForCount keeps only function (custom) tool definitions, which
// contribute to the input-token count, and drops native server tools that the
// count_tokens endpoint rejects.
func functionToolsForCount(tools []schemas.ResponsesTool) []schemas.ResponsesTool {
	var out []schemas.ResponsesTool
	for _, t := range tools {
		if t.Type == schemas.ResponsesToolTypeFunction {
			out = append(out, t)
		}
	}
	return out
}

// multimodalContent creates content blocks for a message with images: the
// message text first, then one block per file — the image as a base64 data URL
// when supported and readable, a placeholder text block otherwise. The block
// type differs between the chat and Responses APIs, so callers supply the
// text- and image-block constructors.
func multimodalContent[T any](post llm.Post, maxDim int, textBlock func(string) T, imageBlock func(dataURL string) T) []T {
	parts := make([]T, 0, len(post.Files)+1)

	if !isBlank(post.Message) {
		parts = append(parts, textBlock(post.Message))
	}

	for _, file := range post.Files {
		if !llm.IsSupportedImageMimeType(file.MimeType) {
			parts = append(parts, textBlock(fmt.Sprintf("[Unsupported image type: %s]", file.MimeType)))
			continue
		}

		data, err := readFileData(file)
		if err != nil {
			parts = append(parts, textBlock("[Error reading image data]"))
			continue
		}

		// The declared type comes from the file extension, not the bytes. A
		// mislabeled image (e.g. a PNG saved as .jpg) is rejected by providers
		// such as Anthropic, which breaks every later turn of the thread, so
		// label the image with the type sniffed from its content instead.
		mimeType := sniffImageMimeType(data)
		if !llm.IsSupportedImageMimeType(mimeType) {
			parts = append(parts, textBlock(fmt.Sprintf("[Unsupported image type: %s]", mimeType)))
			continue
		}

		if maxDim > 0 {
			if imageConfig, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil &&
				(imageConfig.Width > maxDim || imageConfig.Height > maxDim) {
				parts = append(parts, textBlock(fmt.Sprintf(
					"[Image omitted because its dimensions (%dx%d) exceed the maximum allowed size of %d pixels per dimension.]",
					imageConfig.Width,
					imageConfig.Height,
					maxDim,
				)))
				continue
			}
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		parts = append(parts, imageBlock(fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)))
	}

	return parts
}

// sniffImageMimeType returns the media type detected from the content, without
// parameters. http.DetectContentType recognizes every type accepted by
// llm.IsSupportedImageMimeType (JPEG, PNG, GIF and WebP).
func sniffImageMimeType(data []byte) string {
	mimeType, _, _ := strings.Cut(http.DetectContentType(data), ";")
	return strings.TrimSpace(mimeType)
}

func hasToolUseHistory(posts []llm.Post) bool {
	for _, post := range posts {
		if len(post.ToolUse) > 0 {
			return true
		}
	}
	return false
}

func (b *LLM) providerSupportsNativeTools() bool {
	return supportsNativeToolsProvider(b.provider)
}

// shouldUseResponsesAPI determines if the Responses API should be used for this request.
func (b *LLM) shouldUseResponsesAPI(cfg llm.LanguageModelConfig) bool {
	if b.useResponsesAPI {
		return true
	}

	// Direct OpenAI always sets useResponsesAPI during service construction.
	// A false value for OpenAI-base or Azure providers therefore represents an
	// explicit operator choice to use Chat Completions and must not be overridden
	// by native tool configuration.
	if b.provider == schemas.OpenAI || b.provider == schemas.Azure {
		return false
	}

	if b.providerSupportsNativeTools() && len(b.enabledNativeTools) > 0 {
		return true
	}
	if b.providerSupportsNativeTools() && cfg.NativeWebSearchAllowed && !cfg.SkipNativeWebSearch {
		return true
	}
	return false
}

// requestFallbacks returns the fallback chain to attach to this request.
// RestrictFallbacks truncates at the first hop whose service ID is not in
// AllowedFallbackServiceIDs so a denied hop is never skipped over.
func (b *LLM) requestFallbacks(request llm.CompletionRequest) []schemas.Fallback {
	allowed := make(map[string]struct{}, len(request.AllowedFallbackServiceIDs))
	for _, id := range request.AllowedFallbackServiceIDs {
		allowed[id] = struct{}{}
	}

	var out []schemas.Fallback
	for _, hop := range b.fallbacks {
		if _, ok := allowed[hop.serviceID]; request.RestrictFallbacks && !ok {
			break
		}
		out = append(out, hop.fallback)
	}
	return out
}

// isNativeToolEnabled checks if a native tool is enabled by name.
func (b *LLM) isNativeToolEnabled(name string) bool {
	return slices.Contains(b.enabledNativeTools, name)
}
