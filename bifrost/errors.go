// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

const (
	// maxRawErrorBodyLen bounds how much of a provider error body (or a
	// message lifted out of it) is carried into an error string or log line.
	maxRawErrorBodyLen = 2048
	// maxRawErrorFieldLen bounds provider-supplied type/code identifiers.
	maxRawErrorFieldLen = 128
)

// ErrorLogger is the subset of pluginapi.LogService the bifrost package uses
// to write provider error bodies to the server log.
type ErrorLogger interface {
	Error(message string, keyValuePairs ...any)
}

// bifrostErrorString returns a non-empty, admin-readable description of a
// bifrost error: the provider's message followed by every identifying detail
// bifrost carried (HTTP status, error type/code, routed provider).
//
// Error.Message is blank when the provider response body doesn't match
// bifrost's expected error shape (e.g. OpenAI's in-band Responses SSE
// `{"type":"error","error":{...}}` event delivered on HTTP 200), and on
// transport/cancellation paths. In that case fall back to the wrapped Go error,
// then to the message/type/code parsed out of the raw provider body captured in
// ExtraFields.RawResponse (the account enables SendBackRawResponse for exactly
// this reason), before giving up with whatever status/type/code is available.
//
// Only structured fields are returned: the string travels to callers such as
// bridge plugins, so an unparsed body is never included here. It goes to the
// server log via LLM.logProviderErrorBody instead.
func bifrostErrorString(bifrostErr *schemas.BifrostError) string {
	if bifrostErr == nil {
		return "<nil bifrost error>"
	}

	raw := parseRawErrorBody(bifrostErr.ExtraFields.RawResponse)

	msg := ""
	if bifrostErr.Error != nil {
		msg = strings.TrimSpace(bifrostErr.Error.Message)
		if msg == "" && bifrostErr.Error.Error != nil {
			msg = strings.TrimSpace(bifrostErr.Error.Error.Error())
		}
	}
	if msg == "" {
		msg = raw.message
	}
	if msg == "" {
		msg = "empty bifrost error"
	}

	var parts []string
	if bifrostErr.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("status=%d", *bifrostErr.StatusCode))
	}
	if t := errorType(bifrostErr, raw); t != "" {
		parts = append(parts, fmt.Sprintf("type=%s", t))
	}
	if c := errorCode(bifrostErr, raw); c != "" {
		parts = append(parts, fmt.Sprintf("code=%s", c))
	}
	if provider := string(bifrostErr.ExtraFields.RoutingInfo.Provider); provider != "" {
		parts = append(parts, fmt.Sprintf("provider=%s", provider))
	}

	if len(parts) == 0 {
		return msg
	}
	return msg + " (" + strings.Join(parts, " ") + ")"
}

// providerError builds the error returned to callers for a bifrost failure
// and, when bifrost retained the provider's response body, writes that body to
// the server log. The returned error carries only structured fields; the log
// line carries the body, bounded and with configured secrets redacted, so an
// admin can see exactly what the provider said even when it matched none of
// the shapes bifrostErrorString understands. A nil logger skips the log line.
func providerError(logger ErrorLogger, redactKeys []string, prefix string, bifrostErr *schemas.BifrostError) error {
	err := llm.SanitizeProviderError(fmt.Errorf("%s: %s", prefix, bifrostErrorString(bifrostErr)), redactKeys...)
	logProviderErrorBody(logger, redactKeys, err, bifrostErr)
	return err
}

func (b *LLM) providerError(prefix string, bifrostErr *schemas.BifrostError) error {
	return providerError(b.logger, b.redactionKeys(), prefix, bifrostErr)
}

func logProviderErrorBody(logger ErrorLogger, redactKeys []string, err error, bifrostErr *schemas.BifrostError) {
	if logger == nil || bifrostErr == nil {
		return
	}
	raw := parseRawErrorBody(bifrostErr.ExtraFields.RawResponse)
	if raw.body == "" {
		return
	}
	logger.Error("LLM provider returned an error",
		"error", err.Error(),
		"provider_response", llm.SanitizeProviderErrorMessage(truncate(raw.body, maxRawErrorBodyLen), redactKeys...),
	)
}

// rawErrorBody is the subset of a provider error body that is useful in a log
// line. Providers disagree on shape: OpenAI nests under "error", the Responses
// API streams `{"type":"error","code":..,"message":..}` or wraps a failed
// response as `{"response":{"error":{...}}}`, Anthropic nests under "error"
// with its own type. All are probed so the first non-empty value wins.
type rawErrorBody struct {
	body    string
	message string
	typ     string
	code    string
}

type rawErrorEnvelope struct {
	Message  string            `json:"message"`
	Type     string            `json:"type"`
	Code     any               `json:"code"`
	Error    *rawErrorEnvelope `json:"error"`
	Response *struct {
		Error *rawErrorEnvelope `json:"error"`
	} `json:"response"`
}

func parseRawErrorBody(rawResponse any) rawErrorBody {
	var body []byte
	switch v := rawResponse.(type) {
	case nil:
		return rawErrorBody{}
	case json.RawMessage:
		body = v
	case []byte:
		body = v
	case string:
		body = []byte(v)
	default:
		if b, err := json.Marshal(v); err == nil {
			body = b
		}
	}

	out := rawErrorBody{body: strings.TrimSpace(string(body))}
	if out.body == "" {
		return rawErrorBody{}
	}

	var env rawErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return out
	}

	// Walk from the most specific nesting outwards; the outer envelope's
	// generic "type":"error" must not shadow a nested "insufficient_quota".
	candidates := []*rawErrorEnvelope{}
	if env.Response != nil && env.Response.Error != nil {
		candidates = append(candidates, env.Response.Error)
	}
	if env.Error != nil {
		candidates = append(candidates, env.Error)
	}
	candidates = append(candidates, &env)

	for _, c := range candidates {
		if out.message == "" {
			out.message = truncate(strings.TrimSpace(c.Message), maxRawErrorBodyLen)
		}
		if out.typ == "" {
			out.typ = truncate(strings.TrimSpace(c.Type), maxRawErrorFieldLen)
		}
		if out.code == "" {
			out.code = truncate(codeString(c.Code), maxRawErrorFieldLen)
		}
	}
	return out
}

// codeString renders a provider error code, which is a string for most
// providers but a number for some OpenAI-compatible endpoints.
func codeString(code any) string {
	switch v := code.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// errorType prefers the provider's specific error type over the literal
// "error", which is just an SSE event name / envelope marker and carries no
// information about what went wrong.
func errorType(bifrostErr *schemas.BifrostError, raw rawErrorBody) string {
	structured := ""
	if bifrostErr.Error != nil && bifrostErr.Error.Type != nil {
		structured = *bifrostErr.Error.Type
	}
	if structured != "" && structured != "error" {
		return structured
	}
	if raw.typ != "" && raw.typ != "error" {
		return raw.typ
	}
	if structured != "" {
		return structured
	}
	if bifrostErr.Type != nil && *bifrostErr.Type != "" {
		return *bifrostErr.Type
	}
	return raw.typ
}

func errorCode(bifrostErr *schemas.BifrostError, raw rawErrorBody) string {
	if bifrostErr.Error != nil && bifrostErr.Error.Code != nil && *bifrostErr.Error.Code != "" {
		return *bifrostErr.Error.Code
	}
	return raw.code
}

// truncate cuts s to at most limit bytes without splitting a UTF-8 sequence.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated]"
}

// recordBifrostError attaches BifrostError fields to the current span as
// attributes so opaque "bifrost error: …" log lines can be correlated with the
// upstream status / type / code at trace time.
func recordBifrostError(span trace.Span, bifrostErr *schemas.BifrostError) {
	if span == nil || bifrostErr == nil {
		return
	}
	raw := parseRawErrorBody(bifrostErr.ExtraFields.RawResponse)
	attrs := make([]attribute.KeyValue, 0, 5)
	attrs = append(attrs, telemetry.LLMBifrostIsBifrostErr.Bool(bifrostErr.IsBifrostError))
	if bifrostErr.StatusCode != nil {
		attrs = append(attrs, telemetry.LLMBifrostStatusCode.Int(*bifrostErr.StatusCode))
	}
	if t := errorType(bifrostErr, raw); t != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorType.String(t))
	}
	if c := errorCode(bifrostErr, raw); c != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorCode.String(c))
	}
	if provider := string(bifrostErr.ExtraFields.RoutingInfo.Provider); provider != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorProvider.String(provider))
	}
	span.SetAttributes(attrs...)
}

// recordReasoningSent attaches the outbound request's reasoning configuration
// to the current span. Pass nil when no reasoning block is attached.
func recordReasoningSent(span trace.Span, reasoning *schemas.ChatReasoning) {
	if reasoning == nil {
		recordReasoningSentAttrs(span, false, nil, nil)
		return
	}
	recordReasoningSentAttrs(span, true, reasoning.Effort, reasoning.MaxTokens)
}

// recordResponsesReasoningSent is the Responses-API counterpart to
// recordReasoningSent. The Responses parameter type is distinct from
// ChatReasoning so we need a sibling overload.
func recordResponsesReasoningSent(span trace.Span, reasoning *schemas.ResponsesParametersReasoning) {
	if reasoning == nil {
		recordReasoningSentAttrs(span, false, nil, nil)
		return
	}
	recordReasoningSentAttrs(span, true, reasoning.Effort, reasoning.MaxTokens)
}

// recordReasoningSentAttrs is the shared core of recordReasoningSent and
// recordResponsesReasoningSent; sent is false when no reasoning block is
// attached to the request.
func recordReasoningSentAttrs(span trace.Span, sent bool, effort *string, maxTokens *int) {
	if span == nil {
		return
	}
	if !sent {
		span.SetAttributes(telemetry.LLMReasoningSent.Bool(false))
		return
	}
	attrs := []attribute.KeyValue{telemetry.LLMReasoningSent.Bool(true)}
	if effort != nil {
		attrs = append(attrs, telemetry.LLMReasoningEffort.String(*effort))
	}
	if maxTokens != nil {
		attrs = append(attrs, telemetry.LLMReasoningMaxTokens.Int(*maxTokens))
	}
	span.SetAttributes(attrs...)
}
