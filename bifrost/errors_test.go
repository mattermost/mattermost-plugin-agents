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

	t.Run("nil error returns sentinel string", func(t *testing.T) {
		require.Equal(t, "<nil bifrost error>", bifrostErrorString(nil))
	})

	t.Run("message populated returns message", func(t *testing.T) {
		err := &schemas.BifrostError{
			Error: &schemas.ErrorField{Message: "boom"},
		}
		require.Equal(t, "boom", bifrostErrorString(err))
	})

	t.Run("whitespace-only message falls through to wrapped error", func(t *testing.T) {
		err := &schemas.BifrostError{
			Error: &schemas.ErrorField{
				Message: "   ",
				Error:   errors.New("wrapped cause"),
			},
		}
		require.Equal(t, "wrapped cause", bifrostErrorString(err))
	})

	t.Run("message empty but wrapped error populated returns wrapped error", func(t *testing.T) {
		err := &schemas.BifrostError{
			Error: &schemas.ErrorField{Error: errors.New("context deadline exceeded")},
		}
		require.Equal(t, "context deadline exceeded", bifrostErrorString(err))
	})

	t.Run("message and wrapped error empty falls back to status/type/code", func(t *testing.T) {
		err := &schemas.BifrostError{
			StatusCode: intPtr(502),
			Error: &schemas.ErrorField{
				Type: strPtr("upstream_error"),
				Code: strPtr("UPSTREAM_DOWN"),
			},
		}
		require.Equal(t, "empty bifrost error (status=502 type=upstream_error code=UPSTREAM_DOWN)", bifrostErrorString(err))
	})

	t.Run("top-level Type used when ErrorField.Type empty", func(t *testing.T) {
		err := &schemas.BifrostError{
			Type:  strPtr("request_canceled"),
			Error: &schemas.ErrorField{},
		}
		require.Equal(t, "empty bifrost error (type=request_canceled)", bifrostErrorString(err))
	})

	t.Run("all fields empty still returns non-empty fallback", func(t *testing.T) {
		err := &schemas.BifrostError{
			Error: &schemas.ErrorField{},
		}
		require.Equal(t, "empty bifrost error", bifrostErrorString(err))
	})

	t.Run("nil ErrorField still returns non-empty fallback", func(t *testing.T) {
		err := &schemas.BifrostError{StatusCode: intPtr(500)}
		require.Equal(t, "empty bifrost error (status=500)", bifrostErrorString(err))
	})
}
