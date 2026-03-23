// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func TestIsSupportedServiceType(t *testing.T) {
	require.True(t, IsSupportedServiceType(ServiceTypeOpenAICompatible))
	require.True(t, IsSupportedServiceType(string(schemas.Vertex)))
	require.True(t, IsSupportedServiceType(string(schemas.Ollama)))
	require.False(t, IsSupportedServiceType("scale"))
	require.False(t, IsSupportedServiceType("notreal"))
	require.False(t, IsSupportedServiceType("mattermostllm"))
}

func TestModelProviderForServiceType(t *testing.T) {
	provider, err := ModelProviderForServiceType(ServiceTypeOpenAICompatible)
	require.NoError(t, err)
	require.Equal(t, schemas.OpenAI, provider)

	provider, err = ModelProviderForServiceType(string(schemas.Vertex))
	require.NoError(t, err)
	require.Equal(t, schemas.Vertex, provider)

	_, err = ModelProviderForServiceType("scale")
	require.Error(t, err)
}
