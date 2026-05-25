// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
	"strings"
)

// ErrUnsupportedTokenCount signals that the underlying provider does not
// support exact input-token counting. Callers should fall back to
// EstimateTokens (or skip the check entirely) when they see this error.
var ErrUnsupportedTokenCount = errors.New("token counting not supported by this provider")

// EstimateTokens returns a fast, provider-agnostic approximation of the
// tokens needed to encode the given text. The math averages a chars/token
// estimate with a words/token estimate; accuracy is within roughly ±20% for
// English text and degrades on heavily symbolic / non-ASCII content.
//
// This is intentionally cheap and synchronous. Callers that need an exact
// count should use LanguageModel.CountTokens, which calls the provider.
func EstimateTokens(text string) int {
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75
	return int((charCount + wordCount) / 2.0)
}
