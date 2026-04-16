// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// AnnotationType represents different types of annotations
type AnnotationType string

const (
	// AnnotationTypeURLCitation represents a web search citation
	AnnotationTypeURLCitation AnnotationType = "url_citation"
)

// Annotation represents an inline annotation/citation in the response text.
// Indices are stored in JavaScript UTF-16 code units so they can be applied
// directly by the webapp's string slicing.
type Annotation struct {
	Type       AnnotationType `json:"type"`                 // Type of annotation
	StartIndex int            `json:"start_index"`          // Start position in message text (0-based, JS UTF-16 code units)
	EndIndex   int            `json:"end_index"`            // End position in message text (0-based, JS UTF-16 code units)
	URL        string         `json:"url,omitempty"`        // Source URL (for url_citation)
	Title      string         `json:"title,omitempty"`      // Source title (for url_citation)
	CitedText  string         `json:"cited_text,omitempty"` // Optional: text being cited (for context)
	Index      int            `json:"index"`                // Display index (1-based for UI)
}
