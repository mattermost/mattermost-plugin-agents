// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// buildSearchAnnotationsAndCleanText parses !!CITE#!! markers and creates post annotations.
// It returns the annotations and the cleaned message with markers removed.
// The frontend will re-insert markers based on annotations.
func buildSearchAnnotationsAndCleanText(message string, results []RAGResult) ([]llm.Annotation, string) {
	if len(message) == 0 || len(results) == 0 {
		return nil, message
	}

	// Build index map for quick lookup
	indexMap := make(map[int]RAGResult, len(results))
	for _, res := range results {
		indexMap[res.Index] = res
	}

	annotations := []llm.Annotation{}
	var cleanedMessage strings.Builder
	pos := 0
	runeIndex := 0

	for pos < len(message) {
		// Look for "!!CITE" sequence
		if pos+6 <= len(message) && message[pos:pos+6] == "!!CITE" {
			markerStartPos := pos
			markerStartRuneIndex := runeIndex

			// Move past "!!CITE" (6 bytes, 6 runes since all ASCII)
			pos += 6

			// Parse the number
			numBuilder := strings.Builder{}
			digitCursor := pos
			for digitCursor < len(message) {
				digitRune, digitSize := utf8.DecodeRuneInString(message[digitCursor:])
				if digitRune < '0' || digitRune > '9' {
					break
				}
				numBuilder.WriteRune(digitRune)
				digitCursor += digitSize
			}

			if numBuilder.Len() == 0 {
				// No number found, include in cleaned text and continue
				cleanedMessage.WriteString(message[markerStartPos:digitCursor])
				runeIndex += utf8.RuneCountInString(message[markerStartPos:digitCursor])
				pos = digitCursor
				continue
			}

			// Check for closing "!!"
			if digitCursor+2 <= len(message) && message[digitCursor:digitCursor+2] == "!!" {
				nextPos := digitCursor + 2

				idx, err := strconv.Atoi(numBuilder.String())
				if err == nil {
					if res, ok := indexMap[idx]; ok {
						// Found a valid citation - create annotation and DON'T include marker in cleaned text
						annotations = append(annotations, llm.Annotation{
							Type:        llm.AnnotationTypePostCitation,
							StartIndex:  markerStartRuneIndex,
							EndIndex:    markerStartRuneIndex, // Zero-width annotation - frontend will insert marker
							Index:       idx,
							PostID:      res.PostID,
							ChannelID:   res.ChannelID,
							ChannelName: res.ChannelName,
							Username:    res.Username,
							CitedText:   res.Content,
						})
						// Skip the marker in cleaned message - frontend will insert it based on annotation
						pos = nextPos
						continue
					}
				}

				// Not a valid citation, include in cleaned text
				cleanedMessage.WriteString(message[markerStartPos:nextPos])
				runeIndex += utf8.RuneCountInString(message[markerStartPos:nextPos])
				pos = nextPos
				continue
			}

			// Didn't find closing "!!", include in cleaned text
			cleanedMessage.WriteString(message[markerStartPos:digitCursor])
			runeIndex += utf8.RuneCountInString(message[markerStartPos:digitCursor])
			pos = digitCursor
			continue
		}

		// Regular character - add to cleaned message
		r, size := utf8.DecodeRuneInString(message[pos:])
		cleanedMessage.WriteRune(r)
		pos += size
		runeIndex++
	}

	return annotations, cleanedMessage.String()
}

// DecorateSearchStreamWithAnnotations wraps a text stream to emit EventTypeAnnotations
// at the end with post citation annotations based on !!CITE#!! markers in the response.
func DecorateSearchStreamWithAnnotations(result *llm.TextStreamResult, results []RAGResult) *llm.TextStreamResult {
	if result == nil || len(results) == 0 {
		return result
	}

	output := make(chan llm.TextStreamEvent)
	go func() {
		defer close(output)
		var builder strings.Builder

		for event := range result.Stream {
			switch event.Type {
			case llm.EventTypeText:
				if text, ok := event.Value.(string); ok {
					builder.WriteString(text)
				}
				// Pass through text events as normal during streaming
				output <- event
			case llm.EventTypeEnd:
				fullMessage := builder.String()
				annotations, cleanedMessage := buildSearchAnnotationsAndCleanText(fullMessage, results)

				// Send annotations with cleaned message metadata
				if len(annotations) > 0 {
					output <- llm.TextStreamEvent{
						Type: llm.EventTypeAnnotations,
						Value: map[string]interface{}{
							"annotations":    annotations,
							"cleanedMessage": cleanedMessage,
						},
					}
				}
				output <- event
			default:
				output <- event
			}
		}
	}()

	return &llm.TextStreamResult{Stream: output}
}
