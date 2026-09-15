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
			name: "unparseable raw body is echoed verbatim",
			input: &schemas.BifrostError{
				StatusCode: intPtr(502),
				Error:      &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: json.RawMessage(`<html>502 Bad Gateway</html>`),
				},
			},
			expected: "empty bifrost error (status=502 raw=<html>502 Bad Gateway</html>)",
		},
		{
			name: "raw body without any message is echoed verbatim",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: []byte(`{"detail":"Not Found"}`),
				},
			},
			expected: `empty bifrost error (raw={"detail":"Not Found"})`,
		},
		{
			name: "oversized raw body is truncated",
			input: &schemas.BifrostError{
				Error: &schemas.ErrorField{},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RawResponse: strings.Repeat("x", maxRawErrorBodyLen+10),
				},
			},
			expected: "empty bifrost error (raw=" + strings.Repeat("x", maxRawErrorBodyLen) + "…[truncated])",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, bifrostErrorString(tt.input))
		})
	}
}
