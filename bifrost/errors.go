// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// bifrostErrorString returns a non-empty description of a bifrost error.
// Error.Message is blank when the provider response body doesn't match bifrost's
// expected error shape, and on transport/cancellation paths — so fall back to
// the wrapped Go error, then to status/type/code, before giving up.
func bifrostErrorString(bifrostErr *schemas.BifrostError) string {
	if bifrostErr == nil {
		return "<nil bifrost error>"
	}

	if bifrostErr.Error != nil {
		if msg := strings.TrimSpace(bifrostErr.Error.Message); msg != "" {
			return msg
		}
		if bifrostErr.Error.Error != nil {
			if msg := strings.TrimSpace(bifrostErr.Error.Error.Error()); msg != "" {
				return msg
			}
		}
	}

	var parts []string
	if bifrostErr.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("status=%d", *bifrostErr.StatusCode))
	}
	if t := errorType(bifrostErr); t != "" {
		parts = append(parts, fmt.Sprintf("type=%s", t))
	}
	if bifrostErr.Error != nil && bifrostErr.Error.Code != nil && *bifrostErr.Error.Code != "" {
		parts = append(parts, fmt.Sprintf("code=%s", *bifrostErr.Error.Code))
	}

	if len(parts) == 0 {
		return "empty bifrost error"
	}
	return "empty bifrost error (" + strings.Join(parts, " ") + ")"
}

func errorType(bifrostErr *schemas.BifrostError) string {
	if bifrostErr.Error != nil && bifrostErr.Error.Type != nil && *bifrostErr.Error.Type != "" {
		return *bifrostErr.Error.Type
	}
	if bifrostErr.Type != nil && *bifrostErr.Type != "" {
		return *bifrostErr.Type
	}
	return ""
}

// recordBifrostError attaches BifrostError fields to the current span as
// attributes so opaque "bifrost error: …" log lines can be correlated with the
// upstream status / type / code at trace time.
func recordBifrostError(span trace.Span, bifrostErr *schemas.BifrostError) {
	if span == nil || bifrostErr == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 5)
	attrs = append(attrs, telemetry.LLMBifrostIsBifrostErr.Bool(bifrostErr.IsBifrostError))
	if bifrostErr.StatusCode != nil {
		attrs = append(attrs, telemetry.LLMBifrostStatusCode.Int(*bifrostErr.StatusCode))
	}
	if t := errorType(bifrostErr); t != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorType.String(t))
	}
	if bifrostErr.Error != nil && bifrostErr.Error.Code != nil && *bifrostErr.Error.Code != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorCode.String(*bifrostErr.Error.Code))
	}
	if provider := string(bifrostErr.ExtraFields.Provider); provider != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorProvider.String(provider))
	}
	span.SetAttributes(attrs...)
}

// recordReasoningSent attaches the outbound request's reasoning configuration
// to the current span. Pass nil when no reasoning block is attached.
func recordReasoningSent(span trace.Span, reasoning *schemas.ChatReasoning) {
	if span == nil {
		return
	}
	if reasoning == nil {
		span.SetAttributes(telemetry.LLMReasoningSent.Bool(false))
		return
	}
	attrs := []attribute.KeyValue{telemetry.LLMReasoningSent.Bool(true)}
	if reasoning.Effort != nil {
		attrs = append(attrs, telemetry.LLMReasoningEffort.String(*reasoning.Effort))
	}
	if reasoning.MaxTokens != nil {
		attrs = append(attrs, telemetry.LLMReasoningMaxTokens.Int(*reasoning.MaxTokens))
	}
	span.SetAttributes(attrs...)
}

// recordResponsesReasoningSent is the Responses-API counterpart to
// recordReasoningSent. The Responses parameter type is distinct from
// ChatReasoning so we need a sibling overload.
func recordResponsesReasoningSent(span trace.Span, reasoning *schemas.ResponsesParametersReasoning) {
	if span == nil {
		return
	}
	if reasoning == nil {
		span.SetAttributes(telemetry.LLMReasoningSent.Bool(false))
		return
	}
	attrs := []attribute.KeyValue{telemetry.LLMReasoningSent.Bool(true)}
	if reasoning.Effort != nil {
		attrs = append(attrs, telemetry.LLMReasoningEffort.String(*reasoning.Effort))
	}
	if reasoning.MaxTokens != nil {
		attrs = append(attrs, telemetry.LLMReasoningMaxTokens.Int(*reasoning.MaxTokens))
	}
	span.SetAttributes(attrs...)
}
