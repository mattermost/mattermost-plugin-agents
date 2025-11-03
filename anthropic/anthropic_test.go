// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package anthropic

import (
	"bytes"
	"encoding/json"
	"testing"

	anthropicSDK "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// Helper function to create a test message from JSON
func createMessageFromJSON(t *testing.T, jsonStr string) anthropicSDK.Message {
	var msg anthropicSDK.Message
	err := json.Unmarshal([]byte(jsonStr), &msg)
	require.NoError(t, err)
	return msg
}

func TestExtractAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		messageJSON string
		wantResults []llm.Annotation
	}{
		{
			name: "no citations",
			messageJSON: `{
				"id": "msg_test1",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "This is a simple message without citations"
					}
				],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			wantResults: nil,
		},
		{
			name: "single text block with one citation",
			messageJSON: `{
				"id": "msg_test2",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "According to research, AI is advancing rapidly.",
						"citations": [
							{
								"type": "web_search_result_location",
								"url": "https://example.com/ai-research",
								"title": "AI Research Paper",
								"cited_text": "AI is advancing rapidly"
							}
						]
					}
				],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			wantResults: []llm.Annotation{
				{
					Type:       llm.AnnotationTypeURLCitation,
					StartIndex: 0,
					EndIndex:   47,
					URL:        "https://example.com/ai-research",
					Title:      "AI Research Paper",
					CitedText:  "AI is advancing rapidly",
					Index:      1,
				},
			},
		},
		{
			name: "multiple text blocks with citations",
			messageJSON: `{
				"id": "msg_test3",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "First paragraph with citation.",
						"citations": [
							{
								"type": "web_search_result_location",
								"url": "https://example.com/source1",
								"title": "Source 1",
								"cited_text": "citation"
							}
						]
					},
					{
						"type": "text",
						"text": "Second paragraph with another citation.",
						"citations": [
							{
								"type": "web_search_result_location",
								"url": "https://example.com/source2",
								"title": "Source 2",
								"cited_text": "another citation"
							}
						]
					}
				],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			wantResults: []llm.Annotation{
				{
					Type:       llm.AnnotationTypeURLCitation,
					StartIndex: 0,
					EndIndex:   30,
					URL:        "https://example.com/source1",
					Title:      "Source 1",
					CitedText:  "citation",
					Index:      1,
				},
				{
					Type:       llm.AnnotationTypeURLCitation,
					StartIndex: 30,
					EndIndex:   69,
					URL:        "https://example.com/source2",
					Title:      "Source 2",
					CitedText:  "another citation",
					Index:      2,
				},
			},
		},
		{
			name: "multiple citations in one text block",
			messageJSON: `{
				"id": "msg_test4",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "This text has multiple sources cited.",
						"citations": [
							{
								"type": "web_search_result_location",
								"url": "https://example.com/source1",
								"title": "First Source",
								"cited_text": "multiple sources"
							},
							{
								"type": "web_search_result_location",
								"url": "https://example.com/source2",
								"title": "Second Source",
								"cited_text": "cited"
							}
						]
					}
				],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			wantResults: []llm.Annotation{
				{
					Type:       llm.AnnotationTypeURLCitation,
					StartIndex: 0,
					EndIndex:   37,
					URL:        "https://example.com/source1",
					Title:      "First Source",
					CitedText:  "multiple sources",
					Index:      1,
				},
				{
					Type:       llm.AnnotationTypeURLCitation,
					StartIndex: 0,
					EndIndex:   37,
					URL:        "https://example.com/source2",
					Title:      "Second Source",
					CitedText:  "cited",
					Index:      2,
				},
			},
		},
		{
			name: "text blocks without citations mixed with text blocks with citations",
			messageJSON: `{
				"id": "msg_test5",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "Introductory text without citation."
					},
					{
						"type": "text",
						"text": "Text with citation.",
						"citations": [
							{
								"type": "web_search_result_location",
								"url": "https://example.com/cited",
								"title": "Cited Source",
								"cited_text": "citation"
							}
						]
					},
					{
						"type": "text",
						"text": "Concluding text without citation."
					}
				],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			wantResults: []llm.Annotation{
				{
					Type:       llm.AnnotationTypeURLCitation,
					StartIndex: 35,
					EndIndex:   54,
					URL:        "https://example.com/cited",
					Title:      "Cited Source",
					CitedText:  "citation",
					Index:      1,
				},
			},
		},
		{
			name: "empty message",
			messageJSON: `{
				"id": "msg_test6",
				"type": "message",
				"role": "assistant",
				"content": [],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			wantResults: nil,
		},
		{
			name: "non-text content blocks should be ignored",
			messageJSON: `{
				"id": "msg_test7",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "tool_use",
						"id": "tool_123",
						"name": "some_tool",
						"input": {}
					},
					{
						"type": "text",
						"text": "Text after tool use.",
						"citations": [
							{
								"type": "web_search_result_location",
								"url": "https://example.com/after-tool",
								"title": "After Tool Source",
								"cited_text": "tool use"
							}
						]
					}
				],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			wantResults: []llm.Annotation{
				{
					Type:       llm.AnnotationTypeURLCitation,
					StartIndex: 0,
					EndIndex:   20,
					URL:        "https://example.com/after-tool",
					Title:      "After Tool Source",
					CitedText:  "tool use",
					Index:      1,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := createMessageFromJSON(t, tt.messageJSON)
			a := &Anthropic{}
			got := a.extractAnnotations(message)
			assert.Equal(t, tt.wantResults, got)
		})
	}
}

func TestConversationToMessages(t *testing.T) {
	tests := []struct {
		name         string
		conversation []llm.Post
		wantSystem   string
		wantMessages []anthropicSDK.MessageParam
	}{
		{
			name: "basic conversation with system message",
			conversation: []llm.Post{
				{Role: llm.PostRoleSystem, Message: "You are a helpful assistant"},
				{Role: llm.PostRoleUser, Message: "Hello"},
				{Role: llm.PostRoleBot, Message: "Hi there!"},
			},
			wantSystem: "You are a helpful assistant",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Hello"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleAssistant,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Hi there!"),
					},
				},
			},
		},
		{
			name: "multiple messages from same role",
			conversation: []llm.Post{
				{Role: llm.PostRoleUser, Message: "First message"},
				{Role: llm.PostRoleUser, Message: "Second message"},
				{Role: llm.PostRoleBot, Message: "First response"},
				{Role: llm.PostRoleBot, Message: "Second response"},
			},
			wantSystem: "",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("First message"),
						anthropicSDK.NewTextBlock("Second message"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleAssistant,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("First response"),
						anthropicSDK.NewTextBlock("Second response"),
					},
				},
			},
		},
		{
			name: "conversation with image",
			conversation: []llm.Post{
				{Role: llm.PostRoleUser, Message: "Look at this:",
					Files: []llm.File{
						{
							MimeType: "image/jpeg",
							Reader:   bytes.NewReader([]byte("fake-image-data")),
						},
					}},
				{Role: llm.PostRoleBot, Message: "I see the image"},
			},
			wantSystem: "",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Look at this:"),
						anthropicSDK.NewImageBlockBase64("image/jpeg", "ZmFrZS1pbWFnZS1kYXRh"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleAssistant,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("I see the image"),
					},
				},
			},
		},
		{
			name: "unsupported image type",
			conversation: []llm.Post{
				{Role: llm.PostRoleUser, Files: []llm.File{
					{
						MimeType: "image/tiff",
						Reader:   bytes.NewReader([]byte("fake-tiff-data")),
					},
				}},
			},
			wantSystem: "",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("[Unsupported image type: image/tiff]"),
					},
				},
			},
		},
		{
			name: "complex back and forth with repeated roles",
			conversation: []llm.Post{
				{Role: llm.PostRoleUser, Message: "First question"},
				{Role: llm.PostRoleBot, Message: "First answer"},
				{Role: llm.PostRoleUser, Message: "Follow up 1"},
				{Role: llm.PostRoleUser, Message: "Follow up 2"},
				{Role: llm.PostRoleUser, Message: "Follow up 3"},
				{Role: llm.PostRoleBot, Message: "Response 1"},
				{Role: llm.PostRoleBot, Message: "Response 2"},
				{Role: llm.PostRoleBot, Message: "Response 3"},
				{Role: llm.PostRoleUser, Message: "Final question"},
			},
			wantSystem: "",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("First question"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleAssistant,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("First answer"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Follow up 1"),
						anthropicSDK.NewTextBlock("Follow up 2"),
						anthropicSDK.NewTextBlock("Follow up 3"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleAssistant,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Response 1"),
						anthropicSDK.NewTextBlock("Response 2"),
						anthropicSDK.NewTextBlock("Response 3"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Final question"),
					},
				},
			},
		},
		{
			name: "multiple roles with multiple images",
			conversation: []llm.Post{
				{Role: llm.PostRoleUser, Message: "Look at these images:",
					Files: []llm.File{
						{
							MimeType: "image/jpeg",
							Reader:   bytes.NewReader([]byte("image-1")),
						},
						{
							MimeType: "image/png",
							Reader:   bytes.NewReader([]byte("image-2")),
						},
					},
				},
				{Role: llm.PostRoleBot, Message: "I see them"},
				{Role: llm.PostRoleUser, Message: "Here are more:",
					Files: []llm.File{
						{
							MimeType: "image/webp",
							Reader:   bytes.NewReader([]byte("image-3")),
						},
						{
							MimeType: "image/tiff", // unsupported
							Reader:   bytes.NewReader([]byte("image-4")),
						},
						{
							MimeType: "image/gif",
							Reader:   bytes.NewReader([]byte("image-5")),
						},
					},
				},
			},
			wantSystem: "",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Look at these images:"),
						anthropicSDK.NewImageBlockBase64("image/jpeg", "aW1hZ2UtMQ=="),
						anthropicSDK.NewImageBlockBase64("image/png", "aW1hZ2UtMg=="),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleAssistant,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("I see them"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Here are more:"),
						anthropicSDK.NewImageBlockBase64("image/webp", "aW1hZ2UtMw=="),
						anthropicSDK.NewTextBlock("[Unsupported image type: image/tiff]"),
						anthropicSDK.NewImageBlockBase64("image/gif", "aW1hZ2UtNQ=="),
					},
				},
			},
		},
		{
			name: "bot message with tool use and reasoning",
			conversation: []llm.Post{
				{Role: llm.PostRoleUser, Message: "What's the weather?"},
				{
					Role:               llm.PostRoleBot,
					Message:            "Let me check that for you.",
					Reasoning:          "I need to use the weather tool to get current weather information for the user's location.",
					ReasoningSignature: "test_signature_abc123",
					ToolUse: []llm.ToolCall{
						{
							ID:        "call_123",
							Name:      "get_weather",
							Arguments: json.RawMessage(`{"location": "New York"}`),
							Result:    "Sunny, 72°F",
							Status:    llm.ToolCallStatusSuccess,
						},
					},
				},
			},
			wantSystem: "",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("What's the weather?"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleAssistant,
					Content: []anthropicSDK.ContentBlockParamUnion{
						// Thinking block should come first with signature
						anthropicSDK.NewThinkingBlock("test_signature_abc123", "I need to use the weather tool to get current weather information for the user's location."),
						anthropicSDK.NewTextBlock("Let me check that for you."),
						anthropicSDK.NewToolUseBlock("call_123", json.RawMessage(`{"location": "New York"}`), "get_weather"),
					},
				},
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewToolResultBlock("call_123", "Sunny, 72°F", false),
					},
				},
			},
		},
		{
			name: "conversation without system message",
			conversation: []llm.Post{
				{Role: llm.PostRoleUser, Message: "Generate a title for this: Hello world"},
			},
			wantSystem: "",
			wantMessages: []anthropicSDK.MessageParam{
				{
					Role: anthropicSDK.MessageParamRoleUser,
					Content: []anthropicSDK.ContentBlockParamUnion{
						anthropicSDK.NewTextBlock("Generate a title for this: Hello world"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSystem, gotMessages := conversationToMessages(tt.conversation)
			assert.Equal(t, tt.wantSystem, gotSystem)
			assert.Equal(t, tt.wantMessages, gotMessages)
		})
	}
}
