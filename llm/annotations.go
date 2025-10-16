// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// AnnotationType represents different types of annotations
type AnnotationType string

const (
	// AnnotationTypeURLCitation represents a web search citation
	AnnotationTypeURLCitation AnnotationType = "url_citation"
)

// Annotation represents an inline annotation/citation in the response text
type Annotation struct {
	Type       AnnotationType `json:"type"`                 // Type of annotation
	StartIndex int            `json:"start_index"`          // Start position in message text (0-based)
	EndIndex   int            `json:"end_index"`            // End position in message text (0-based)
	URL        string         `json:"url"`                  // Source URL
	Title      string         `json:"title"`                // Source title
	CitedText  string         `json:"cited_text,omitempty"` // Optional: text being cited (for context)
	Index      int            `json:"index"`                // Display index (1-based for UI)
}
