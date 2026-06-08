// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"sync"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// turnIDKey types the ctx key under which we stash the user turn ID.
// Unexported so callers must go through WithTurnID.
type turnIDKey struct{}

// WithTurnID returns a ctx that, when used to start a new root span (via
// trace.WithNewRoot), will produce a TraceID deterministically derived from
// turnID. Empty turnID is a no-op.
func WithTurnID(ctx context.Context, turnID string) context.Context {
	if turnID == "" {
		return ctx
	}
	return context.WithValue(ctx, turnIDKey{}, turnID)
}

// SpanContextForTurn returns a valid SpanContext anchored to the trace
// associated with turnID, suitable for use as a trace.Link target. The
// SpanID matches what the run's root span would have, so a link points to
// a real, locatable trace in Tempo even though the link's exact span ID
// won't necessarily resolve.
func SpanContextForTurn(turnID string) trace.SpanContext {
	if turnID == "" {
		return trace.SpanContext{}
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    deriveTraceID(turnID),
		SpanID:     deriveSpanID(turnID, "root"),
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

// NewTurnIDGenerator returns an IDGenerator suitable for sdktrace.WithIDGenerator
// that produces deterministic IDs derived from the turn ID stashed in ctx via
// WithTurnID, and falls back to the SDK's default random generator otherwise.
func NewTurnIDGenerator() sdktrace.IDGenerator {
	return &turnIDGenerator{fallback: defaultIDGenerator()}
}

type turnIDGenerator struct {
	fallback sdktrace.IDGenerator
}

func (g *turnIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	turnID, ok := ctx.Value(turnIDKey{}).(string)
	if !ok || turnID == "" {
		return g.fallback.NewIDs(ctx)
	}
	tid := deriveTraceID(turnID)
	sid := deriveSpanID(turnID, "root")
	if !tid.IsValid() || !sid.IsValid() {
		// Vanishingly unlikely (would require all-zero SHA-256 prefix), but
		// keep the SpanContext valid by falling back when it happens.
		return g.fallback.NewIDs(ctx)
	}
	return tid, sid
}

func (g *turnIDGenerator) NewSpanID(ctx context.Context, traceID trace.TraceID) trace.SpanID {
	// Child spans always get random IDs; only roots are derived.
	return g.fallback.NewSpanID(ctx, traceID)
}

func deriveTraceID(turnID string) trace.TraceID {
	h := sha256.Sum256([]byte("trace:" + turnID))
	var tid trace.TraceID
	copy(tid[:], h[:16])
	return tid
}

func deriveSpanID(turnID, salt string) trace.SpanID {
	h := sha256.Sum256([]byte("span:" + salt + ":" + turnID))
	var sid trace.SpanID
	copy(sid[:], h[:8])
	return sid
}

// defaultIDGenerator returns a random IDGenerator equivalent to the
// SDK's built-in default. Used as the fallback path when ctx carries no
// turn ID.
func defaultIDGenerator() sdktrace.IDGenerator {
	return &randomIDGenerator{}
}

type randomIDGenerator struct {
	mu sync.Mutex
}

func (g *randomIDGenerator) NewIDs(_ context.Context) (trace.TraceID, trace.SpanID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var tid trace.TraceID
	for {
		_, _ = rand.Read(tid[:])
		if tid.IsValid() {
			break
		}
	}
	var sid trace.SpanID
	for {
		_, _ = rand.Read(sid[:])
		if sid.IsValid() {
			break
		}
	}
	return tid, sid
}

func (g *randomIDGenerator) NewSpanID(_ context.Context, _ trace.TraceID) trace.SpanID {
	g.mu.Lock()
	defer g.mu.Unlock()
	var sid trace.SpanID
	for {
		_, _ = rand.Read(sid[:])
		if sid.IsValid() {
			break
		}
	}
	return sid
}
