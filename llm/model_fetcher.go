// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// ModelInfo represents information about an available model
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}
