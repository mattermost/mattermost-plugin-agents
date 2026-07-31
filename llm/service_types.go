// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

const (
	ServiceTypeOpenAI           = "openai"
	ServiceTypeOpenAICompatible = "openaicompatible"
	ServiceTypeAzure            = "azure"
	ServiceTypeAnthropic        = "anthropic"
	ServiceTypeCohere           = "cohere"
	ServiceTypeBedrock          = "bedrock"
	ServiceTypeMistral          = "mistral"
	ServiceTypeScale            = "scale"
	ServiceTypeGemini           = "gemini"
	ServiceTypeVertex           = "vertex"
	ServiceTypeLoadTestMock     = "loadtest_mock"
)

// Native (provider-executed) tool ids stored in BotConfig.EnabledNativeTools.
// The same ids are used by the webapp's native-tools checklist. Which ids each
// service type supports is defined by bifrost.SupportedNativeToolsForServiceType.
const (
	// NativeToolWebSearch is provider web search (OpenAI web_search, Anthropic
	// web_search, Gemini/Vertex Google Search grounding).
	NativeToolWebSearch = "web_search"
	// NativeToolWebFetch is Anthropic's web_fetch server tool: retrieve the full
	// content of specific web pages / PDFs.
	NativeToolWebFetch = "web_fetch"
	// NativeToolFileSearch is OpenAI's file_search tool over vector stores.
	NativeToolFileSearch = "file_search"
	// NativeToolCodeInterpreter is the provider code sandbox: OpenAI's
	// code_interpreter, or Anthropic's code_execution tool. For Anthropic this
	// also opts web_search/web_fetch into sandbox-based dynamic filtering (see
	// bifrost.convertToResponsesTools).
	NativeToolCodeInterpreter = "code_interpreter"
)
