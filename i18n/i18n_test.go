// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package i18n

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalizerFunc(t *testing.T) {
	bundle := Init()

	tests := []struct {
		name           string
		bundle         *Bundle
		lang           string
		id             string
		defaultMessage string
		params         []any
		want           string
	}{
		{
			name:           "Spanish translation loaded from embedded es.json",
			bundle:         bundle,
			lang:           "es",
			id:             "agents.no_longer_access_error",
			defaultMessage: "Sorry, you no longer have access to the original thread.",
			want:           "Lo siento, ya no tiene acceso al hilo original.",
		},
		{
			name:           "Regional locale falls back to primary language",
			bundle:         bundle,
			lang:           "es_MX",
			id:             "agents.stream_to_post_llm_not_return",
			defaultMessage: "Sorry! The LLM did not return a result.",
			want:           "Lo siento, el LLM no devolvió resultados.",
		},
		{
			name:           "Spanish translation formats sprintf parameters",
			bundle:         bundle,
			lang:           "es-ES",
			id:             "agents.summarize_recording",
			defaultMessage: "Sure, I will summarize this recording: %s/_redirect/pl/%s\n",
			params:         []any{"https://mm.example.com", "post123"},
			want:           "Claro, resumiré esta grabación: https://mm.example.com/_redirect/pl/post123\n",
		},
		{
			name:           "Unknown translation ID falls back to default message",
			bundle:         bundle,
			lang:           "es",
			id:             "agents.unknown_key",
			defaultMessage: "Fallback %d",
			params:         []any{42},
			want:           "Fallback 42",
		},
		{
			name:           "Unknown locale falls back to default message",
			bundle:         bundle,
			lang:           "fr",
			id:             "agents.no_longer_access_error",
			defaultMessage: "Default text",
			want:           "Default text",
		},
		{
			name:           "Nil bundle falls back to default message",
			bundle:         nil,
			lang:           "es",
			id:             "agents.no_longer_access_error",
			defaultMessage: "Default %s",
			params:         []any{"value"},
			want:           "Default value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			T := LocalizerFunc(tc.bundle, tc.lang)
			assert.Equal(t, tc.want, T(tc.id, tc.defaultMessage, tc.params...))
		})
	}
}
