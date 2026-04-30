// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errStreamingTimeout = errors.New("timeout streaming")

func TestSanitizeProviderError(t *testing.T) {
	t.Run("preserves unrelated errors", func(t *testing.T) {
		sanitizedErr := SanitizeProviderError(errStreamingTimeout, "")

		assert.Same(t, errStreamingTimeout, sanitizedErr)
	})

	t.Run("redacts auth material from provider errors", func(t *testing.T) {
		configuredKey := "this-is-my-disclosed-api-key"

		tests := []struct {
			name            string
			input           string
			wantContains    string
			wantNotContains []string
		}{
			{
				name:         "incorrect api key message",
				input:        `{"error":{"message":"Incorrect API key provided: this-is-my-disclosed-api-key. You can find your API key at https://platform.openai.com/account/api-keys.","type":"invalid_request_error","code":"invalid_api_key"}}`,
				wantContains: `Incorrect API key provided. You can find your API key`,
				wantNotContains: []string{
					"this-is-my-disclosed-api-key",
				},
			},
			{
				name:         "progressively masked key",
				input:        `{"error":{"message":"Incorrect API key provided: this-is-****************-key. You can find your API key at https://platform.openai.com/account/api-keys.","type":"invalid_request_error","code":"invalid_api_key"}}`,
				wantContains: `Incorrect API key provided. You can find your API key`,
				wantNotContains: []string{
					"this-is-****************-key",
				},
			},
			{
				name:         "authorization header",
				input:        `upstream failure: Authorization: Bearer sk-proj-1234567890abcdefghijklmnop`,
				wantContains: `Authorization: Bearer [REDACTED]`,
				wantNotContains: []string{
					"sk-proj-1234567890abcdefghijklmnop",
				},
			},
			{
				name:         "standalone openai key token",
				input:        `provider error: leaked sk-1234567890abcdefghij token`,
				wantContains: `provider error: leaked [REDACTED] token`,
				wantNotContains: []string{
					"sk-1234567890abcdefghij",
				},
			},
			{
				name:         "standalone anthropic key token",
				input:        `provider error: leaked sk-ant-1234567890abcdefghijklmnop`,
				wantContains: `provider error: leaked [REDACTED]`,
				wantNotContains: []string{
					"sk-ant-1234567890abcdefghijklmnop",
				},
			},
			{
				name:         "json api key field",
				input:        `{"apiKey":"this-is-my-disclosed-api-key","detail":"request failed"}`,
				wantContains: `"apiKey":"[REDACTED]"`,
				wantNotContains: []string{
					"this-is-my-disclosed-api-key",
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				sanitizedErr := SanitizeProviderError(errors.New(tt.input), configuredKey)
				require.NotNil(t, sanitizedErr)
				assert.Contains(t, sanitizedErr.Error(), tt.wantContains)
				for _, secret := range tt.wantNotContains {
					assert.NotContains(t, sanitizedErr.Error(), secret)
				}
			})
		}
	})

	t.Run("redacts short configured api keys", func(t *testing.T) {
		sanitizedErr := SanitizeProviderError(errors.New(`provider error: short`), "short")
		require.NotNil(t, sanitizedErr)
		assert.Equal(t, "provider error: [REDACTED]", sanitizedErr.Error())
	})

	t.Run("does not corrupt unrelated words for one character keys", func(t *testing.T) {
		providerErrorMessage := `Unauthorized: Incorrect API key provided: t. You can find your API key at https://platform.openai.com/account/api-keys.`

		sanitizedErr := SanitizeProviderError(errors.New(providerErrorMessage), "t")
		require.NotNil(t, sanitizedErr)
		assert.Equal(t, "Unauthorized: Incorrect API key provided. You can find your API key at https://platform.openai.com/account/api-keys.", sanitizedErr.Error())
		assert.NotContains(t, sanitizedErr.Error(), "Unau[REDACTED]horized")
		assert.NotContains(t, sanitizedErr.Error(), "Incorrect API key provided: t")
	})

	t.Run("preserves wrapped provider error chain", func(t *testing.T) {
		originalErr := errors.New("provider error: short")

		sanitizedErr := SanitizeProviderError(originalErr, "short")
		require.NotNil(t, sanitizedErr)
		assert.Equal(t, "provider error: [REDACTED]", sanitizedErr.Error())
		assert.ErrorIs(t, sanitizedErr, originalErr)

		var wrapped *SanitizedProviderError
		require.ErrorAs(t, sanitizedErr, &wrapped)
		assert.Equal(t, "provider error: [REDACTED]", wrapped.Error())
		assert.Equal(t, originalErr, wrapped.Unwrap())
	})
}

func TestSanitizeProviderError_bifrostStylePrefixes(t *testing.T) {
	const key = "this-is-my-disclosed-api-key"
	raw := `Incorrect API key provided: this-is-my-disclosed-api-key. You can find your API key at https://platform.openai.com/account/api-keys.`

	err := SanitizeProviderError(fmt.Errorf("bifrost error: %s", raw), key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bifrost error:")
	assert.Contains(t, err.Error(), "Incorrect API key provided.")
	assert.NotContains(t, err.Error(), key)
}
