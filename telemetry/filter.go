// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// abacDecisionSpans are the per-request access decisions. Policy
// administration spans (abac save_policy, …) are rare and always kept, as is
// abac is_available, which only gets a span on an uncached probe (at most
// one per cache TTL).
var abacDecisionSpans = map[string]bool{
	"abac can_use_agent":      true,
	"abac can_use_service":    true,
	"abac can_use_mcp_server": true,
}

// keepSpan reports whether a finished span is worth exporting. Access
// control checks run on nearly every request (agent listings, tool catalogs)
// and outnumber everything else by orders of magnitude, so a routine allow
// is dropped. Denials and evaluation failures are kept: those are the checks
// someone debugging a missing agent or MCP server needs to see.
func keepSpan(s sdktrace.ReadOnlySpan) bool {
	if !abacDecisionSpans[s.Name()] {
		return true
	}
	if s.Status().Code == codes.Error {
		return true
	}
	for _, a := range s.Attributes() {
		if a.Key == ABACDenied && a.Value.AsBool() {
			return true
		}
	}
	return false
}

// filteringExporter applies keepSpan in front of another exporter so the
// OTLP output mode drops the same spans as the server-logs mode.
type filteringExporter struct {
	next sdktrace.SpanExporter
}

func newFilteringExporter(next sdktrace.SpanExporter) sdktrace.SpanExporter {
	return &filteringExporter{next: next}
}

func (e *filteringExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	kept := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, s := range spans {
		if keepSpan(s) {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return e.next.ExportSpans(ctx, kept)
}

func (e *filteringExporter) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}
