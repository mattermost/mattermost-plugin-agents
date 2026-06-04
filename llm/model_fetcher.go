// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// ModelInfo represents information about an available model. The pointer
// limit fields are nil when the provider doesn't report them.
type ModelInfo struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	InputTokenLimit  *int   `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit *int   `json:"outputTokenLimit,omitempty"`
	ContextLength    *int   `json:"contextLength,omitempty"`
}
