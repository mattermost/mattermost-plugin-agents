// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestValidateDictation(t *testing.T) {
	services := []llm.ServiceConfig{
		{ID: "openai1", Name: "OpenAI", Type: llm.ServiceTypeOpenAI, APIKey: "k"},
		{ID: "proxied", Name: "Proxied", Type: llm.ServiceTypeOpenAI, APIKey: "k", APIURL: "https://proxy.example.com/v1"},
		{ID: "nokey", Name: "No key", Type: llm.ServiceTypeOpenAI},
		{ID: "creds", Name: "Creds", Type: llm.ServiceTypeOpenAI, APIKey: "k", APIURL: "https://user:secret@proxy.example.com"},
		{ID: "query", Name: "Query", Type: llm.ServiceTypeOpenAI, APIKey: "k", APIURL: "https://proxy.example.com?token=secret"},
		{ID: "relative", Name: "Relative", Type: llm.ServiceTypeOpenAI, APIKey: "k", APIURL: "//proxy.example.com"},
		{ID: "ftp", Name: "FTP", Type: llm.ServiceTypeOpenAI, APIKey: "k", APIURL: "ftp://proxy.example.com"},
		{ID: "anthropic1", Name: "Anthropic", Type: llm.ServiceTypeAnthropic, APIKey: "k"},
		{ID: "azure1", Name: "Azure", Type: llm.ServiceTypeAzure, APIKey: "k", APIURL: "https://res.openai.azure.com"},
	}

	tests := []struct {
		name      string
		dictation DictationConfig
		wantErr   bool
	}{
		{name: "disabled with nothing set", dictation: DictationConfig{}},
		{name: "disabled with a stale service", dictation: DictationConfig{ServiceID: "gone"}},
		{name: "openai service", dictation: DictationConfig{Enabled: true, ServiceID: "openai1", Model: "gpt-live-transcribe"}},
		{name: "no service", dictation: DictationConfig{Enabled: true, Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "missing service", dictation: DictationConfig{Enabled: true, ServiceID: "gone", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "wrong service type", dictation: DictationConfig{Enabled: true, ServiceID: "anthropic1", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "azure is not yet supported", dictation: DictationConfig{Enabled: true, ServiceID: "azure1", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "openai service behind a proxy URL", dictation: DictationConfig{Enabled: true, ServiceID: "proxied", Model: "gpt-live-transcribe"}},
		{name: "service without an API key", dictation: DictationConfig{Enabled: true, ServiceID: "nokey", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "API URL with credentials", dictation: DictationConfig{Enabled: true, ServiceID: "creds", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "API URL with a query", dictation: DictationConfig{Enabled: true, ServiceID: "query", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "protocol-relative API URL", dictation: DictationConfig{Enabled: true, ServiceID: "relative", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "non-http API URL", dictation: DictationConfig{Enabled: true, ServiceID: "ftp", Model: "gpt-live-transcribe"}, wantErr: true},
		{name: "empty model", dictation: DictationConfig{Enabled: true, ServiceID: "openai1", Model: "  "}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Services: services, Dictation: tt.dictation}
			err := cfg.ValidateDictation()
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidDictationConfig)
		})
	}
}
