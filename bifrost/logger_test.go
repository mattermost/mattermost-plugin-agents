// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturedLogEntry struct {
	level   string
	message string
	fields  map[string]string
	ints    map[string]int
	int64s  map[string]int64
}

type capturedLogger struct {
	entries    []capturedLogEntry
	level      schemas.LogLevel
	outputType schemas.LoggerOutputType
}

func (l *capturedLogger) Debug(msg string, args ...any) {
	l.entries = append(l.entries, capturedLogEntry{
		level:   "debug",
		message: fmt.Sprintf(msg, args...),
	})
}

func (l *capturedLogger) Info(msg string, args ...any) {
	l.entries = append(l.entries, capturedLogEntry{
		level:   "info",
		message: fmt.Sprintf(msg, args...),
	})
}

func (l *capturedLogger) Warn(msg string, args ...any) {
	l.entries = append(l.entries, capturedLogEntry{
		level:   "warn",
		message: fmt.Sprintf(msg, args...),
	})
}

func (l *capturedLogger) Error(msg string, args ...any) {
	l.entries = append(l.entries, capturedLogEntry{
		level:   "error",
		message: fmt.Sprintf(msg, args...),
	})
}

func (l *capturedLogger) Fatal(msg string, args ...any) {
	l.entries = append(l.entries, capturedLogEntry{
		level:   "fatal",
		message: fmt.Sprintf(msg, args...),
	})
}

func (l *capturedLogger) SetLevel(level schemas.LogLevel) {
	l.level = level
}

func (l *capturedLogger) SetOutputType(outputType schemas.LoggerOutputType) {
	l.outputType = outputType
}

func (l *capturedLogger) LogHTTPRequest(level schemas.LogLevel, msg string) schemas.LogEventBuilder {
	return &capturedLogEventBuilder{
		logger: l,
		entry: capturedLogEntry{
			level:   string(level),
			message: msg,
			fields:  map[string]string{},
			ints:    map[string]int{},
			int64s:  map[string]int64{},
		},
	}
}

type capturedLogEventBuilder struct {
	logger *capturedLogger
	entry  capturedLogEntry
}

func (b *capturedLogEventBuilder) Str(key, val string) schemas.LogEventBuilder {
	b.entry.fields[key] = val
	return b
}

func (b *capturedLogEventBuilder) Int(key string, val int) schemas.LogEventBuilder {
	b.entry.ints[key] = val
	return b
}

func (b *capturedLogEventBuilder) Int64(key string, val int64) schemas.LogEventBuilder {
	b.entry.int64s[key] = val
	return b
}

func (b *capturedLogEventBuilder) Send() {
	b.logger.entries = append(b.logger.entries, b.entry)
}

func TestSanitizingLoggerSanitizesFormattedMessages(t *testing.T) {
	const configuredKey = "this-is-my-disclosed-api-key"

	inner := &capturedLogger{}
	logger := newSanitizingLogger(inner, configuredKey)

	logger.Warn("failed to list models with key %s: %s",
		"key-id",
		`Incorrect API key provided: this-is-my-disclosed-api-key. You can find your API key at https://platform.openai.com/account/api-keys.`,
	)

	require.Len(t, inner.entries, 1)
	assert.Equal(t, "warn", inner.entries[0].level)
	assert.Equal(t,
		"failed to list models with key key-id: Incorrect API key provided. You can find your API key at https://platform.openai.com/account/api-keys.",
		inner.entries[0].message,
	)
	assert.NotContains(t, inner.entries[0].message, configuredKey)
}

func TestSanitizingLoggerRedactsProviderSecretsAcrossLevels(t *testing.T) {
	tests := []struct {
		name            string
		logFn           func(schemas.Logger, string, ...any)
		wantLevel       string
		message         string
		wantContains    string
		wantNotContains []string
	}{
		{
			name:         "debug redacts partially masked incorrect key message",
			logFn:        func(logger schemas.Logger, msg string, args ...any) { logger.Debug(msg, args...) },
			wantLevel:    "debug",
			message:      `Incorrect API key provided: poc test******osed. You can find your API key at https://platform.openai.com/account/api-keys.`,
			wantContains: `Incorrect API key provided. You can find your API key`,
			wantNotContains: []string{
				"poc test******osed",
			},
		},
		{
			name:         "info redacts bearer token",
			logFn:        func(logger schemas.Logger, msg string, args ...any) { logger.Info(msg, args...) },
			wantLevel:    "info",
			message:      `provider failure: Authorization: Bearer sk-proj-1234567890abcdefghijklmnop`,
			wantContains: `Authorization: Bearer [REDACTED]`,
			wantNotContains: []string{
				"sk-proj-1234567890abcdefghijklmnop",
			},
		},
		{
			name:         "error redacts anthropic token",
			logFn:        func(logger schemas.Logger, msg string, args ...any) { logger.Error(msg, args...) },
			wantLevel:    "error",
			message:      `provider failure: leaked sk-ant-1234567890abcdefghijklmnop`,
			wantContains: `provider failure: leaked [REDACTED]`,
			wantNotContains: []string{
				"sk-ant-1234567890abcdefghijklmnop",
			},
		},
		{
			name:         "fatal redacts configured key",
			logFn:        func(logger schemas.Logger, msg string, args ...any) { logger.Fatal(msg, args...) },
			wantLevel:    "fatal",
			message:      `provider failure: this-is-my-disclosed-api-key`,
			wantContains: `provider failure: [REDACTED]`,
			wantNotContains: []string{
				"this-is-my-disclosed-api-key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &capturedLogger{}
			logger := newSanitizingLogger(inner, "this-is-my-disclosed-api-key")

			tt.logFn(logger, tt.message)

			require.Len(t, inner.entries, 1)
			assert.Equal(t, tt.wantLevel, inner.entries[0].level)
			assert.Contains(t, inner.entries[0].message, tt.wantContains)
			for _, secret := range tt.wantNotContains {
				assert.NotContains(t, inner.entries[0].message, secret)
			}
		})
	}
}

func TestSanitizingLoggerSanitizesHTTPRequestLogs(t *testing.T) {
	const configuredKey = "this-is-my-disclosed-api-key"

	inner := &capturedLogger{}
	logger := newSanitizingLogger(inner, configuredKey)

	logger.LogHTTPRequest(
		schemas.LogLevelWarn,
		`Incorrect API key provided: this-is-my-disclosed-api-key. You can find your API key at https://platform.openai.com/account/api-keys.`,
	).
		Str("authorization", "Authorization: Bearer sk-proj-1234567890abcdefghijklmnop").
		Str("configured_api_key", configuredKey).
		Int("status_code", 401).
		Int64("latency_ms", 123).
		Send()

	require.Len(t, inner.entries, 1)
	entry := inner.entries[0]
	assert.Equal(t, string(schemas.LogLevelWarn), entry.level)
	assert.Contains(t, entry.message, "Incorrect API key provided.")
	assert.NotContains(t, entry.message, configuredKey)
	assert.Equal(t, "Authorization: Bearer [REDACTED]", entry.fields["authorization"])
	assert.Equal(t, "[REDACTED]", entry.fields["configured_api_key"])
	assert.Equal(t, 401, entry.ints["status_code"])
	assert.EqualValues(t, 123, entry.int64s["latency_ms"])
}
