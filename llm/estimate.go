// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
	"strings"
)

// ErrUnsupportedTokenCount signals that the underlying provider does not
// support exact input-token counting. Callers should fall back to
// EstimateTokens when they see this error.
var ErrUnsupportedTokenCount = errors.New("token counting not supported by this provider")

// EstimateTokens is a fast, synchronous, provider-agnostic approximation.
// For an exact count, call LanguageModel.CountTokens instead.
func EstimateTokens(text string) int {
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75
	return int((charCount + wordCount) / 2.0)
}
