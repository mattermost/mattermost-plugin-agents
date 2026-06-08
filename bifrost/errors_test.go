// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"errors"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, bifrostErrorString(tt.input))
		})
	}
}
