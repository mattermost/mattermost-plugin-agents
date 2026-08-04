// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

type sanitizingLogger struct {
	inner   schemas.Logger
	apiKeys []string
}

func newSanitizingLogger(inner schemas.Logger, apiKeys ...string) schemas.Logger {
	return &sanitizingLogger{
		inner:   inner,
		apiKeys: apiKeys,
	}
}

// newBifrostClient initializes a Bifrost client whose logger redacts the given
// API keys. Pass the primary key plus every fallback key so a fallback
// provider's credential never leaks through Bifrost's own request/error logs.
func newBifrostClient(account schemas.Account, apiKeys ...string) (*bifrostcore.Bifrost, error) {
	return bifrostcore.Init(context.Background(), schemas.BifrostConfig{
		Account: account,
		Logger:  newSanitizingLogger(bifrostcore.NewDefaultLogger(schemas.LogLevelInfo), apiKeys...),
		Tracer:  newOTelTracer(),
	})
}

func (l *sanitizingLogger) Debug(msg string, args ...any) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.Debug(l.sanitizeMessage(msg, args...))
}

func (l *sanitizingLogger) Info(msg string, args ...any) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.Info(l.sanitizeMessage(msg, args...))
}

func (l *sanitizingLogger) Warn(msg string, args ...any) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.Warn(l.sanitizeMessage(msg, args...))
}

func (l *sanitizingLogger) Error(msg string, args ...any) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.Error(l.sanitizeMessage(msg, args...))
}

func (l *sanitizingLogger) Fatal(msg string, args ...any) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.Fatal(l.sanitizeMessage(msg, args...))
}

func (l *sanitizingLogger) SetLevel(level schemas.LogLevel) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.SetLevel(level)
}

func (l *sanitizingLogger) SetOutputType(outputType schemas.LoggerOutputType) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.SetOutputType(outputType)
}

func (l *sanitizingLogger) LogHTTPRequest(level schemas.LogLevel, msg string) schemas.LogEventBuilder {
	if l == nil || l.inner == nil {
		return schemas.NoopLogEvent
	}

	return &sanitizingLogEventBuilder{
		inner:   l.inner.LogHTTPRequest(level, l.sanitizeMessage(msg)),
		apiKeys: l.apiKeys,
	}
}

func (l *sanitizingLogger) sanitizeMessage(msg string, args ...any) string {
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	return llm.SanitizeProviderErrorMessage(msg, l.apiKeys...)
}

type sanitizingLogEventBuilder struct {
	inner   schemas.LogEventBuilder
	apiKeys []string
}

func (b *sanitizingLogEventBuilder) Str(key, val string) schemas.LogEventBuilder {
	if b == nil || b.inner == nil {
		return schemas.NoopLogEvent
	}

	b.inner.Str(key, llm.SanitizeProviderErrorMessage(val, b.apiKeys...))
	return b
}

func (b *sanitizingLogEventBuilder) Int(key string, val int) schemas.LogEventBuilder {
	if b == nil || b.inner == nil {
		return schemas.NoopLogEvent
	}

	b.inner.Int(key, val)
	return b
}

func (b *sanitizingLogEventBuilder) Int64(key string, val int64) schemas.LogEventBuilder {
	if b == nil || b.inner == nil {
		return schemas.NoopLogEvent
	}

	b.inner.Int64(key, val)
	return b
}

func (b *sanitizingLogEventBuilder) Send() {
	if b == nil || b.inner == nil {
		return
	}

	b.inner.Send()
}
