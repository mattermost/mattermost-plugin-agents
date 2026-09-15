// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// maxRawErrorBodyLen bounds how much of an unparseable provider error body is
// echoed into a log line.
const maxRawErrorBodyLen = 2048

// bifrostErrorString returns a non-empty, admin-readable description of a
// bifrost error: the provider's message followed by every identifying detail
// bifrost carried (HTTP status, error type/code, routed provider).
//
// Error.Message is blank when the provider response body doesn't match
// bifrost's expected error shape (e.g. OpenAI's in-band Responses SSE
// `{"type":"error","error":{...}}` event delivered on HTTP 200), and on
// transport/cancellation paths. In that case fall back to the wrapped Go error,
// then to the raw provider body captured in ExtraFields.RawResponse (the
// account enables SendBackRawResponse for exactly this reason), before giving
// up with whatever status/type/code is available.
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

	if msg == "" {
		msg = "empty bifrost error"
		// Nothing structured could be extracted; the raw body is the only clue
		// left for the admin, so surface it verbatim (bounded).
		if raw.body != "" {
			parts = append(parts, fmt.Sprintf("raw=%s", truncate(raw.body, maxRawErrorBodyLen)))
		}
	}

	if len(parts) == 0 {
		return msg
	}
	return msg + " (" + strings.Join(parts, " ") + ")"
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
	Message  string `json:"message"`
	Type     string `json:"type"`
	Code     any    `json:"code"`
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
			out.message = strings.TrimSpace(c.Message)
		}
		if out.typ == "" {
			out.typ = strings.TrimSpace(c.Type)
		}
		if out.code == "" {
			out.code = codeString(c.Code)
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

// errorType prefers the provider's specific error type over bifrost's
// top-level Type, which on the in-band SSE path is just the literal event
// name "error".
func errorType(bifrostErr *schemas.BifrostError, raw rawErrorBody) string {
	if bifrostErr.Error != nil && bifrostErr.Error.Type != nil && *bifrostErr.Error.Type != "" {
		return *bifrostErr.Error.Type
	}
	if raw.typ != "" && raw.typ != "error" {
		return raw.typ
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

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…[truncated]"
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
