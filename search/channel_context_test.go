// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type staticSearchChannelContextProvider struct {
	context llm.ChannelContext
	err     error
}

func (p *staticSearchChannelContextProvider) GetPromptContext(string) (llm.ChannelContext, error) {
	return p.context, p.err
}

func TestBuildSearchPromptContextLoadsChannelContext(t *testing.T) {
	mm := mmapimocks.NewMockClient(t)
	mm.On("GetConfig").Return(&model.Config{})
	provider := &staticSearchChannelContextProvider{context: llm.ChannelContext{
		CustomInstructions: "Use channel terminology.",
		KnowledgeFiles:     "Name: guide.pdf",
	}}
	s := &Search{mmclient: mm}
	s.SetChannelContextProvider(provider)

	context := s.buildSearchPromptContext(model.NewId(), nil, "question", model.NewId(), model.NewId(), nil)

	require.Equal(t, provider.context, context.ChannelContext)
}

func TestBuildSearchPromptContextIgnoresChannelContextFailure(t *testing.T) {
	mm := mmapimocks.NewMockClient(t)
	mm.On("GetConfig").Return(&model.Config{})
	mm.On("LogWarn", "Failed to load channel context for search", mock.Anything).Return()
	s := &Search{mmclient: mm}
	s.SetChannelContextProvider(&staticSearchChannelContextProvider{err: errors.New("database unavailable")})

	context := s.buildSearchPromptContext(model.NewId(), nil, "question", model.NewId(), model.NewId(), nil)

	require.Empty(t, context.ChannelContext)
}
