// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// ModelInfo represents information about an available model.
// InputTokenLimit / OutputTokenLimit / ContextLength are populated when the
// provider reports them via Bifrost's ListModels response, otherwise nil.
type ModelInfo struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	InputTokenLimit  *int   `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit *int   `json:"outputTokenLimit,omitempty"`
	ContextLength    *int   `json:"contextLength,omitempty"`
}
