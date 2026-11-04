// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// providerError converts a bifrost error into the error returned to callers,
// with redactKeys scrubbed, and records it on span when span is non-nil.
func providerError(span trace.Span, prefix string, bifrostErr *schemas.BifrostError, redactKeys ...string) error {
	details := describeBifrostError(bifrostErr)
	err := llm.SanitizeProviderError(fmt.Errorf("%s: %s", prefix, details), redactKeys...)
	if span != nil {
		span.SetAttributes(details.attributes()...)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

type bifrostErrorDetails struct {
	message        string
	status         *int
	typ            string
	code           string
	provider       string
	isBifrostError bool
}

// describeBifrostError falls back to the wrapped Go error because
// Error.Message is blank on transport and cancellation paths.
func describeBifrostError(bifrostErr *schemas.BifrostError) bifrostErrorDetails {
	if bifrostErr == nil {
		return bifrostErrorDetails{message: "<nil bifrost error>"}
	}

	details := bifrostErrorDetails{
		status:         bifrostErr.StatusCode,
		provider:       string(bifrostErr.ExtraFields.RoutingInfo.Provider),
		isBifrostError: bifrostErr.IsBifrostError,
	}
	var errorType string
	if bifrostErr.Error != nil {
		details.message = strings.TrimSpace(bifrostErr.Error.Message)
		if details.message == "" && bifrostErr.Error.Error != nil {
			details.message = strings.TrimSpace(bifrostErr.Error.Error.Error())
		}
		errorType = derefString(bifrostErr.Error.Type)
		details.code = derefString(bifrostErr.Error.Code)
	}
	details.message = cmp.Or(details.message, "empty bifrost error")
	details.typ = cmp.Or(errorType, derefString(bifrostErr.Type))
	return details
}

func (d bifrostErrorDetails) String() string {
	var parts []string
	if d.status != nil {
		parts = append(parts, fmt.Sprintf("status=%d", *d.status))
	}
	if d.typ != "" {
		parts = append(parts, "type="+d.typ)
	}
	if d.code != "" {
		parts = append(parts, "code="+d.code)
	}
	if d.provider != "" {
		parts = append(parts, "provider="+d.provider)
	}
	if len(parts) == 0 {
		return d.message
	}
	return d.message + " (" + strings.Join(parts, " ") + ")"
}

func (d bifrostErrorDetails) attributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{telemetry.LLMBifrostIsBifrostErr.Bool(d.isBifrostError)}
	if d.status != nil {
		attrs = append(attrs, telemetry.LLMBifrostStatusCode.Int(*d.status))
	}
	if d.typ != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorType.String(d.typ))
	}
	if d.code != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorCode.String(d.code))
	}
	if d.provider != "" {
		attrs = append(attrs, telemetry.LLMBifrostErrorProvider.String(d.provider))
	}
	return attrs
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
