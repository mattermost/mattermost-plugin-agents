// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

type webSearchFallbackSource struct {
	URL   string
	Title string
}

type pendingAnnotationPosition struct {
	index        int
	missingStart bool
	missingEnd   bool
}

const missingContentIndex = -1

// convertBifrostAnnotation converts a Bifrost annotation to llm.Annotation
func convertBifrostAnnotation(ann *schemas.ResponsesOutputMessageContentTextAnnotation, index int) *llm.Annotation {
	if ann == nil || ann.Type != "url_citation" {
		return nil
	}

	result := &llm.Annotation{
		Type:  llm.AnnotationTypeURLCitation,
		Index: index,
	}

	if ann.StartIndex != nil {
		result.StartIndex = *ann.StartIndex
	}
	if ann.EndIndex != nil {
		result.EndIndex = *ann.EndIndex
	}
	if ann.URL != nil {
		result.URL = *ann.URL
	}
	if ann.Title != nil {
		result.Title = *ann.Title
	}
	if ann.Text != nil {
		result.CitedText = *ann.Text
	}

	return result
}

func appendFirstWebSearchFallbackSource(sources []webSearchFallbackSource, item *schemas.ResponsesMessage) []webSearchFallbackSource {
	if item == nil || item.Type == nil || *item.Type != schemas.ResponsesMessageTypeWebSearchCall {
		return sources
	}
	if item.Action == nil || item.Action.ResponsesWebSearchToolCallAction == nil {
		return sources
	}

	for _, source := range item.Action.ResponsesWebSearchToolCallAction.Sources {
		if source.URL == "" || hasFallbackSource(sources, source.URL) {
			continue
		}
		title := ""
		if source.Title != nil {
			title = *source.Title
		}
		sources = append(sources, webSearchFallbackSource{
			URL:   source.URL,
			Title: title,
		})
	}
	return sources
}

func hasFallbackSource(sources []webSearchFallbackSource, url string) bool {
	for _, source := range sources {
		if source.URL == url {
			return true
		}
	}
	return false
}

func buildFallbackAnnotations(sources []webSearchFallbackSource, endIndex int) []llm.Annotation {
	annotations := make([]llm.Annotation, 0, len(sources))
	for i, source := range sources {
		annotations = append(annotations, llm.Annotation{
			Type:       llm.AnnotationTypeURLCitation,
			StartIndex: endIndex,
			EndIndex:   endIndex,
			URL:        source.URL,
			Title:      source.Title,
			Index:      i + 1,
		})
	}
	return annotations
}

func applyPendingAnnotationPositions(annotations []llm.Annotation, positions []pendingAnnotationPosition, startIndex, endIndex int) {
	for _, position := range positions {
		if position.index < 0 || position.index >= len(annotations) {
			continue
		}
		if position.missingStart {
			annotations[position.index].StartIndex = startIndex
		}
		if position.missingEnd {
			annotations[position.index].EndIndex = endIndex
		}
	}
}

func flushPendingAnnotationPositions(
	annotations []llm.Annotation,
	pending map[int][]pendingAnnotationPosition,
	contentIndex, startIndex, endIndex int,
) {
	applyPendingAnnotationPositions(annotations, pending[contentIndex], startIndex, endIndex)
	delete(pending, contentIndex)
}
