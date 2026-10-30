// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsAccessLevelFor(t *testing.T) {
	readOnly := model.NewId()
	readWrite := model.NewId()
	both := model.NewId()
	access := SettingsAccess{
		ReadOnlyUserIDs:  []string{readOnly, both},
		ReadWriteUserIDs: []string{readWrite, both},
	}

	tests := []struct {
		name   string
		userID string
		want   SettingsAccessLevel
	}{
		{name: "read-only user", userID: readOnly, want: SettingsAccessRead},
		{name: "read/write user", userID: readWrite, want: SettingsAccessWrite},
		{name: "user on both lists gets read/write", userID: both, want: SettingsAccessWrite},
		{name: "user on neither list", userID: model.NewId(), want: SettingsAccessNone},
		{name: "no user", userID: "", want: SettingsAccessNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, access.LevelFor(tt.userID))
		})
	}
}

func TestContainerSettingsAccessWithoutLists(t *testing.T) {
	var c Container
	c.Update(&Config{})
	assert.Equal(t, SettingsAccessNone, c.SettingsAccess().LevelFor(model.NewId()))
}

func TestWithoutSecrets(t *testing.T) {
	cfg := &Config{
		Services: []llm.ServiceConfig{{ID: "s1", Name: "Primary", APIKey: "secret", AWSAccessKeyID: "secret", AWSSecretAccessKey: "secret", VertexAuthCredentials: "secret", VertexProjectID: "project"}},
		Bots:     []llm.BotConfig{{ID: "b1", Service: &llm.ServiceConfig{APIKey: "secret"}}},
		MCP: MCPConfig{Servers: []MCPServerConfig{{
			Name:                  "Tracker",
			Headers:               map[string]string{"X-Api-Key": "secret"},
			ServiceAccountHeaders: map[string]string{"Authorization": "secret"},
			ClientID:              "client",
			ClientSecret:          "secret",
		}}},
		WebSearch: WebSearchConfig{
			Google: WebSearchGoogleConfig{APIKey: "secret", SearchEngineID: "engine"},
			Brave:  WebSearchBraveConfig{APIKey: "secret"},
		},
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{Parameters: json.RawMessage(`{"apiKey":"secret","model":"m","auth":{"accessToken":"secret","region":"r"}}`)},
			VectorStore:       embeddings.UpstreamConfig{Parameters: json.RawMessage(`"not an object: secret"`)},
		},
		OpenTelemetryHeaders:  "secret",
		OpenTelemetryEndpoint: "collector:4317",
	}

	redacted := cfg.WithoutSecrets()

	body, err := json.Marshal(redacted)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "secret")

	// Everything that is not a credential is still there.
	assert.Equal(t, "Primary", redacted.Services[0].Name)
	assert.Equal(t, "project", redacted.Services[0].VertexProjectID)
	assert.Equal(t, map[string]string{"X-Api-Key": ""}, redacted.MCP.Servers[0].Headers)
	assert.Equal(t, "client", redacted.MCP.Servers[0].ClientID)
	assert.Equal(t, "engine", redacted.WebSearch.Google.SearchEngineID)
	assert.Equal(t, "collector:4317", redacted.OpenTelemetryEndpoint)
	assert.JSONEq(t, `{"apiKey":"","model":"m","auth":{"accessToken":"","region":"r"}}`, string(redacted.EmbeddingSearchConfig.EmbeddingProvider.Parameters))

	// The original is untouched.
	assert.Equal(t, "secret", cfg.Services[0].APIKey)
	assert.Equal(t, "secret", cfg.MCP.Servers[0].Headers["X-Api-Key"])
}

func TestSettingsAccessNormalize(t *testing.T) {
	a := model.NewId()
	b := model.NewId()

	tests := []struct {
		name    string
		in      SettingsAccess
		want    SettingsAccess
		wantErr bool
	}{
		{
			name: "nil lists become empty",
			in:   SettingsAccess{},
			want: SettingsAccess{ReadOnlyUserIDs: []string{}, ReadWriteUserIDs: []string{}},
		},
		{
			name: "duplicates are dropped",
			in:   SettingsAccess{ReadOnlyUserIDs: []string{a, a}, ReadWriteUserIDs: []string{b, b}},
			want: SettingsAccess{ReadOnlyUserIDs: []string{a}, ReadWriteUserIDs: []string{b}},
		},
		{
			name: "read/write users leave the read-only list",
			in:   SettingsAccess{ReadOnlyUserIDs: []string{a, b}, ReadWriteUserIDs: []string{b}},
			want: SettingsAccess{ReadOnlyUserIDs: []string{a}, ReadWriteUserIDs: []string{b}},
		},
		{
			name:    "usernames are rejected",
			in:      SettingsAccess{ReadOnlyUserIDs: []string{"alice"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.Normalize()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
