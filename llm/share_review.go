// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// ShareReview marks tool results that may hold content some members of the
// destination channel cannot read, so they wait for the requester's share
// decision.
type ShareReview struct {
	// Sources are the channels the results drew from that the destination's
	// members may not be able to read. Empty when the results' sources could
	// not be determined.
	Sources []SourceChannel
}

// SourceChannel is a Mattermost channel a tool result drew content from.
type SourceChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}
