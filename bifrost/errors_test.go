// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func TestBifrostErrorString(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	intPtr := func(i int) *int { return &i }

	tests := []struct {
		name     string
		input    *schemas.BifrostError
		expected string
	}{
		{
			name:     "nil error returns sentinel string",
			input:    nil,
			expected: "<nil bifrost error>",
		},
		{
			name: "message populated returns message",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{Message: "boom"},
			},
			expected: "boom",
		},
		{
			name: "whitespace-only message falls through to wrapped error",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{
					Message: "   ",
					Error:   errors.New("wrapped cause"),
				},
			},
			expected: "wrapped cause",
		},
		{
			name: "message empty but wrapped error populated returns wrapped error",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{Error: errors.New("context deadline exceeded")},
			},
			expected: "context deadline exceeded",
		},
		{
			name: "message is followed by status/type/code/provider context",
			input: &schemas.BifrostError{
				StatusCode: intPtr(429),
				Error: &schemas.ErrorField{
					Message: "You exceeded your current quota",
					Type:    strPtr("insufficient_quota"),
					Code:    strPtr("insufficient_quota"),
				},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RoutingInfo: schemas.RoutingInfo{Provider: schemas.OpenAI},
				},
			},
			expected: "You exceeded your current quota (status=429 type=insufficient_quota code=insufficient_quota provider=openai)",
		},
		{
			name: "message and wrapped error empty falls back to status/type/code",
			input: &schemas.BifrostError{
				StatusCode: intPtr(502),
				Error: &schemas.ErrorField{
					Type: strPtr("upstream_error"),
					Code: strPtr("UPSTREAM_DOWN"),
				},
			},
			expected: "empty bifrost error (status=502 type=upstream_error code=UPSTREAM_DOWN)",
		},
		{
			name: "top-level Type used when ErrorField.Type empty",
			input: &schemas.BifrostError{
				Type:  strPtr("request_canceled"),
				Error: &schemas.ErrorField{},
			},
			expected: "empty bifrost error (type=request_canceled)",
		},
		{
			name: "all fields empty still returns non-empty fallback",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{},
			},
			expected: "empty bifrost error",
		},
		{
			name:     "nil ErrorField still returns non-empty fallback",
			input:    &schemas.BifrostError{StatusCode: intPtr(500)},
			expected: "empty bifrost error (status=500)",
		},
		{
			name: "OpenAI in-band Responses SSE error is recovered from the raw body",
			input: &schemas.BifrostError{
				Type:  strPtr("error"),
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`{"type":"error","error":{"type":"insufficient_quota","code":"insufficient_quota","message":"You exceeded your current quota, please check your plan and billing details.","param":null},"sequence_number":2}`),
				},
			},
			expected: "You exceeded your current quota, please check your plan and billing details. (type=insufficient_quota code=insufficient_quota)",
		},
		{
			name: "Responses API response.failed envelope is recovered from the raw body",
			input: &schemas.BifrostError{
				Type:  strPtr("response.failed"),
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"The model produced invalid output."}}}`),
				},
			},
			expected: "The model produced invalid output. (type=response.failed code=server_error)",
		},
		{
			name: "raw body with top-level message and numeric code",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`{"type":"error","code":503,"message":"model overloaded"}`),
				},
			},
			expected: "model overloaded (type=error code=503)",
		},
		{
			name: "structured fields win over raw body",
			input: &schemas.BifrostError{
				StatusCode: intPtr(401),
				Error: &schemas.ErrorField{
					Message: "Incorrect API key provided",
					Type:    strPtr("invalid_request_error"),
					Code:    strPtr("invalid_api_key"),
				},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`{"error":{"message":"other","type":"other_type","code":"other_code"}}`),
				},
			},
			expected: "Incorrect API key provided (status=401 type=invalid_request_error code=invalid_api_key)",
		},
		{
			name: "unparseable raw body is never placed in the returned string",
			input: &schemas.BifrostError{
				StatusCode: intPtr(502),
				Error:      &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`<html>502 Bad Gateway</html>`),
				},
			},
			expected: "empty bifrost error (status=502)",
		},
		{
			name: "raw body without a recognised message is never placed in the returned string",
			input: &schemas.BifrostError{
				Type:  strPtr("error"),
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: []byte(`{"type":"error","detail":"secret internal diagnostics"}`),
				},
			},
			expected: "empty bifrost error (type=error)",
		},
		{
			name: "oversized raw message is bounded",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: []byte(`{"error":{"message":"` + strings.Repeat("m", maxRawErrorBodyLen+10) + `","type":"` + strings.Repeat("t", maxRawErrorFieldLen+10) + `","code":"` + strings.Repeat("c", maxRawErrorFieldLen+10) + `"}}`),
				},
			},
			expected: strings.Repeat("m", maxRawErrorBodyLen) + "…[truncated] (type=" + strings.Repeat("t", maxRawErrorFieldLen) + "…[truncated] code=" + strings.Repeat("c", maxRawErrorFieldLen) + "…[truncated])",
		},
		{
			name: "structured literal error type does not shadow the provider's specific type",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{Type: strPtr("error")},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`{"error":{"type":"insufficient_quota","message":"quota"}}`),
				},
			},
			expected: "quota (type=insufficient_quota)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, bifrostErrorString(tt.input))
		})
	}
}

type recordingLogger struct {
	entries []recordedLog
}

type recordedLog struct {
	message string
	kv      map[string]any
}

func (l *recordingLogger) Error(message string, keyValuePairs ...any) {
	kv := make(map[string]any, len(keyValuePairs)/2)
	for i := 0; i+1 < len(keyValuePairs); i += 2 {
		kv[keyValuePairs[i].(string)] = keyValuePairs[i+1]
	}
	l.entries = append(l.entries, recordedLog{message: message, kv: kv})
}

func TestProviderErrorLogsRawBodySeparately(t *testing.T) {
	const apiKey = "sk-proj-SECRETKEY1234567890"

	tests := []struct {
		name             string
		input            *schemas.BifrostError
		wantErr          string
		wantLogged       bool
		wantBodyContains []string
		wantBodyAbsent   []string
	}{
		{
			name: "unrecognised body goes to the log, not the error",
			input: &schemas.BifrostError{
				Type:  schemas.Ptr("error"),
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`{"type":"error","detail":"upstream exploded"}`),
				},
			},
			wantErr:          "bifrost stream error: empty bifrost error (type=error)",
			wantLogged:       true,
			wantBodyContains: []string{`"detail":"upstream exploded"`},
		},
		{
			name: "configured API key is redacted from the logged body",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`{"error":{"message":"Incorrect API key provided: ` + apiKey + `"}}`),
				},
			},
			wantErr:          "bifrost stream error: Incorrect API key provided",
			wantLogged:       true,
			wantBodyContains: []string{"Incorrect API key provided"},
			wantBodyAbsent:   []string{apiKey, "SECRETKEY"},
		},
		{
			name: "oversized body is bounded in the log",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: strings.Repeat("x", maxRawErrorBodyLen+10),
				},
			},
			wantErr:          "bifrost stream error: empty bifrost error",
			wantLogged:       true,
			wantBodyContains: []string{"…[truncated]"},
			wantBodyAbsent:   []string{strings.Repeat("x", maxRawErrorBodyLen+1)},
		},
		{
			name: "no raw body means no extra log line",
			input: &schemas.BifrostError{
				StatusCode: schemas.Ptr(500),
				Error:      &schemas.ErrorField{Message: "boom"},
			},
			wantErr:    "bifrost stream error: boom (status=500)",
			wantLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &recordingLogger{}
			b := &LLM{apiKey: apiKey, logger: logger}

			err := b.providerError("bifrost stream error", tt.input)
			require.EqualError(t, err, tt.wantErr)

			if !tt.wantLogged {
				require.Empty(t, logger.entries)
				return
			}
			require.Len(t, logger.entries, 1)
			entry := logger.entries[0]
			require.Equal(t, err.Error(), entry.kv["error"])
			body, _ := entry.kv["provider_response"].(string)
			for _, want := range tt.wantBodyContains {
				require.Contains(t, body, want)
			}
			for _, absent := range tt.wantBodyAbsent {
				require.NotContains(t, body, absent)
			}
		})
	}

	t.Run("nil logger is a no-op", func(t *testing.T) {
		b := &LLM{apiKey: apiKey}
		err := b.providerError("bifrost error", &schemas.BifrostError{
			Error:       &schemas.ErrorField{},
			ExtraFields: schemas.BifrostErrorExtraFields{RawResponse: json.RawMessage(`{"detail":"x"}`)},
		})
		require.EqualError(t, err, "bifrost error: empty bifrost error")
	})
}
