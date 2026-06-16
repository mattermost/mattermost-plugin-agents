// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"regexp"
	"strings"
)

const providerErrorRedacted = "[REDACTED]"

var (
	openAIAuthHeaderPattern   = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)(\S+)`)
	openAIJSONAPIKeyPattern   = regexp.MustCompile(`(?i)("api(?:_|)key"\s*:\s*")([^"]+)(")`)
	openAIIncorrectKeyPattern = regexp.MustCompile(`(?i)(Incorrect API key provided)(?::\s*[^"\r\n]+?)?(\.?\s+You can find your API key|["\r\n]|$)`)
	openAIKeyPattern          = regexp.MustCompile(`\bsk(?:-proj)?-[A-Za-z0-9_-]{10,}\b`)
	anthropicKeyPattern       = regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)
)

// SanitizedProviderError wraps an upstream LLM error after redacting secrets from its message.
// It implements [errors.Unwrap] so [errors.Is] / [errors.As] chains on the original error are preserved.
type SanitizedProviderError struct {
	message string
	err     error
}

func (e *SanitizedProviderError) Error() string {
	return e.message
}

func (e *SanitizedProviderError) Unwrap() error {
	return e.err
}

// SanitizeProviderErrorMessage applies the same redaction rules as [SanitizeProviderError] to a plain string.
// Each configured API key is additionally redacted when it appears as a substring (word-boundary safe).
// Pass the primary key plus any fallback keys so a fallback provider's credential in a custom key format
// (e.g. Gemini, Mistral, or a local OpenAI-compatible key) is redacted even when the generic patterns miss it.
func SanitizeProviderErrorMessage(message string, configuredAPIKeys ...string) string {
	sanitized := sanitizeProviderErrorMessagePlain(message)
	for _, key := range configuredAPIKeys {
		apiKey := strings.TrimSpace(key)
		if apiKey != "" {
			sanitized = replaceConfiguredAPIKeyInMessage(sanitized, apiKey)
		}
	}
	return sanitized
}

// SanitizeProviderError redacts API keys, bearer tokens, and similar material from provider errors
// before those strings are logged, streamed to clients, or returned to callers.
func SanitizeProviderError(err error, configuredAPIKeys ...string) error {
	if err == nil {
		return nil
	}

	sanitized := SanitizeProviderErrorMessage(err.Error(), configuredAPIKeys...)
	if sanitized == err.Error() {
		return err
	}

	return &SanitizedProviderError{
		message: sanitized,
		err:     err,
	}
}

func sanitizeProviderErrorMessagePlain(message string) string {
	sanitized := openAIAuthHeaderPattern.ReplaceAllString(message, `${1}`+providerErrorRedacted)
	sanitized = openAIJSONAPIKeyPattern.ReplaceAllString(sanitized, `${1}`+providerErrorRedacted+`${3}`)
	sanitized = openAIIncorrectKeyPattern.ReplaceAllString(sanitized, `${1}`+`${2}`)
	sanitized = openAIKeyPattern.ReplaceAllString(sanitized, providerErrorRedacted)
	sanitized = anthropicKeyPattern.ReplaceAllString(sanitized, providerErrorRedacted)
	return SanitizeNonPrintableChars(sanitized)
}

func replaceConfiguredAPIKeyInMessage(message string, apiKey string) string {
	pattern := regexp.MustCompile(`(^|[^A-Za-z0-9_-])(` + regexp.QuoteMeta(apiKey) + `)([^A-Za-z0-9_-]|$)`)
	return pattern.ReplaceAllString(message, `${1}`+providerErrorRedacted+`${3}`)
}
