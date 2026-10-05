// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"errors"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type fakeAgentDocumentSource struct {
	texts map[string]string
	err   error
	calls int
}

func (s *fakeAgentDocumentSource) AgentDocumentTexts(_ stdcontext.Context, ids []string) (map[string]string, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if text, ok := s.texts[id]; ok {
			out[id] = text
		}
	}
	return out, nil
}

func newDocumentsTestBuilder(t *testing.T, source AgentDocumentTextSource) *Builder {
	t.Helper()
	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	t.Cleanup(func() { mockAPI.AssertExpectations(t) })

	builder := NewLLMContextBuilder(pluginapi.NewClient(mockAPI, nil), &emptyToolProvider{}, nil, &contextTestConfigProvider{})
	builder.SetAgentDocumentSource(source)
	return builder
}

func TestWithLLMContextAgentDocuments(t *testing.T) {
	handbook := llm.AgentDocument{ID: "doc-handbook", Name: "handbook.pdf"}
	faq := llm.AgentDocument{ID: "doc-faq", Name: "faq.md"}
	missing := llm.AgentDocument{ID: "doc-missing", Name: "gone.txt"}
	texts := map[string]string{"doc-handbook": "Handbook text", "doc-faq": "FAQ text"}

	tests := []struct {
		name      string
		documents []llm.AgentDocument
		source    *fakeAgentDocumentSource
		want      []format.AgentDocumentEntry
		wantLoads int
	}{
		{
			name:      "documents are rendered in the agent's order",
			documents: []llm.AgentDocument{faq, handbook},
			source:    &fakeAgentDocumentSource{texts: texts},
			want:      []format.AgentDocumentEntry{{Name: "faq.md", Text: "FAQ text"}, {Name: "handbook.pdf", Text: "Handbook text"}},
			wantLoads: 1,
		},
		{
			name:      "a missing document is skipped",
			documents: []llm.AgentDocument{handbook, missing},
			source:    &fakeAgentDocumentSource{texts: texts},
			want:      []format.AgentDocumentEntry{{Name: "handbook.pdf", Text: "Handbook text"}},
			wantLoads: 1,
		},
		{
			name:      "a load failure leaves the documents out",
			documents: []llm.AgentDocument{handbook},
			source:    &fakeAgentDocumentSource{err: errors.New("database unavailable")},
			wantLoads: 1,
		},
		{
			name:      "an agent without documents loads nothing",
			source:    &fakeAgentDocumentSource{texts: texts},
			wantLoads: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newDocumentsTestBuilder(t, tt.source)
			bot := newTestBotWithConfig(llm.BotConfig{ID: "bot-id", Name: "matty", Documents: tt.documents})
			c := builder.BuildLLMContextUserRequest(stdcontext.Background(), bot, testUser(), testChannel())

			assert.Equal(t, format.AgentReferenceDocuments(tt.want), c.ReferenceDocuments)
			assert.Equal(t, tt.wantLoads, tt.source.calls)
		})
	}
}

func TestReferenceDocumentsRenderedAfterCustomInstructions(t *testing.T) {
	source := &fakeAgentDocumentSource{texts: map[string]string{"doc-handbook": "Refunds are processed within 5 days."}}
	builder := newDocumentsTestBuilder(t, source)

	bot := newTestBotWithConfig(llm.BotConfig{
		ID: "bot-id", Name: "matty", DisplayName: "Matty",
		CustomInstructions: "Always answer politely.",
		Documents:          []llm.AgentDocument{{ID: "doc-handbook", Name: "handbook.pdf"}},
	})
	c := builder.BuildLLMContextUserRequest(stdcontext.Background(), bot, testUser(), testChannel())

	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)
	system, err := promptsEngine.Format(prompts.PromptDirectMessageQuestionSystem, c)
	require.NoError(t, err)

	instructions := strings.Index(system, "Always answer politely.")
	document := strings.Index(system, "<document name=\"handbook.pdf\">\nRefunds are processed within 5 days.\n</document>")
	require.NotEqual(t, -1, instructions, system)
	require.NotEqual(t, -1, document, system)
	assert.Greater(t, document, instructions, "documents follow the custom instructions")
}
