// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSecretPlaceholder(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "the placeholder itself", value: SecretPlaceholder, want: true},
		{name: "shorter run of asterisks is a real value", value: "**********", want: false},
		{name: "single asterisk is a real value", value: "*", want: false},
		{name: "empty", value: "", want: false},
		{name: "credential", value: "sk-live-1234", want: false},
		{name: "credential containing asterisks", value: "sk-**-1234", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsSecretPlaceholder(tt.value))
		})
	}
}

func TestRedactSecrets(t *testing.T) {
	stored := Config{
		Services: []llm.ServiceConfig{{
			ID:                    "svc-1",
			APIKey:                "service-key",
			OrgID:                 "org-1",
			AWSAccessKeyID:        "AKIAEXAMPLE",
			AWSSecretAccessKey:    "aws-secret",
			VertexAuthCredentials: `{"type":"service_account"}`,
		}},
		Bots: []llm.BotConfig{{
			ID:      "bot-1",
			Service: &llm.ServiceConfig{ID: "svc-legacy", APIKey: "inline-key"},
		}},
		MCP: MCPConfig{Servers: []MCPServerConfig{{
			Name:         "Jira",
			BaseURL:      "https://mcp.example.com",
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			Headers:      map[string]string{"Authorization": "Bearer header-token"},
		}}},
		WebSearch: WebSearchConfig{
			Google: WebSearchGoogleConfig{APIKey: "google-key", SearchEngineID: "engine-id"},
			Brave:  WebSearchBraveConfig{APIKey: ""},
		},
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			EmbeddingProvider: embeddings.UpstreamConfig{
				Parameters: json.RawMessage(`{"apiKey":"embedding-key","embeddingModel":"text-embedding-3-small"}`),
			},
		},
	}

	redacted := RedactSecrets(stored)

	tests := []struct {
		name string
		got  func(cfg Config) string
		want string
	}{
		{name: "service api key", got: func(cfg Config) string { return cfg.Services[0].APIKey }, want: SecretPlaceholder},
		{name: "service aws secret", got: func(cfg Config) string { return cfg.Services[0].AWSSecretAccessKey }, want: SecretPlaceholder},
		{name: "service vertex credentials", got: func(cfg Config) string { return cfg.Services[0].VertexAuthCredentials }, want: SecretPlaceholder},
		{name: "inline bot service api key", got: func(cfg Config) string { return cfg.Bots[0].Service.APIKey }, want: SecretPlaceholder},
		{name: "mcp client secret", got: func(cfg Config) string { return cfg.MCP.Servers[0].ClientSecret }, want: SecretPlaceholder},
		{name: "mcp header value", got: func(cfg Config) string { return cfg.MCP.Servers[0].Headers["Authorization"] }, want: SecretPlaceholder},
		{name: "google api key", got: func(cfg Config) string { return cfg.WebSearch.Google.APIKey }, want: SecretPlaceholder},
		{name: "unset brave api key stays empty", got: func(cfg Config) string { return cfg.WebSearch.Brave.APIKey }, want: ""},
		{name: "service org id", got: func(cfg Config) string { return cfg.Services[0].OrgID }, want: "org-1"},
		{name: "service aws access key id", got: func(cfg Config) string { return cfg.Services[0].AWSAccessKeyID }, want: "AKIAEXAMPLE"},
		{name: "mcp client id", got: func(cfg Config) string { return cfg.MCP.Servers[0].ClientID }, want: "client-id"},
		{name: "mcp base url", got: func(cfg Config) string { return cfg.MCP.Servers[0].BaseURL }, want: "https://mcp.example.com"},
		{name: "google search engine id", got: func(cfg Config) string { return cfg.WebSearch.Google.SearchEngineID }, want: "engine-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got(redacted))
		})
	}

	t.Run("embedding provider parameters", func(t *testing.T) {
		params := map[string]string{}
		require.NoError(t, json.Unmarshal(redacted.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
		assert.Equal(t, SecretPlaceholder, params["apiKey"])
		assert.Equal(t, "text-embedding-3-small", params["embeddingModel"])
	})

	t.Run("stored configuration is untouched", func(t *testing.T) {
		assert.Equal(t, "service-key", stored.Services[0].APIKey)
		assert.Equal(t, "inline-key", stored.Bots[0].Service.APIKey)
		assert.Equal(t, "Bearer header-token", stored.MCP.Servers[0].Headers["Authorization"])
	})
}

func TestRestoreSecrets(t *testing.T) {
	storedConfig := func() *Config {
		return &Config{
			Services: []llm.ServiceConfig{{
				ID:                    "svc-1",
				APIKey:                "service-key",
				AWSSecretAccessKey:    "aws-secret",
				VertexAuthCredentials: `{"type":"service_account"}`,
			}},
			Bots: []llm.BotConfig{{
				ID:      "bot-1",
				Service: &llm.ServiceConfig{ID: "svc-legacy", APIKey: "inline-key"},
			}},
			MCP: MCPConfig{Servers: []MCPServerConfig{{
				Name:         "Jira",
				ClientSecret: "client-secret",
				Headers: map[string]string{
					"Authorization": "Bearer header-token",
					"X-Tenant":      "tenant-1",
				},
			}}},
			WebSearch: WebSearchConfig{
				Google: WebSearchGoogleConfig{APIKey: "google-key"},
				Brave:  WebSearchBraveConfig{APIKey: "brave-key"},
			},
			EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
				EmbeddingProvider: embeddings.UpstreamConfig{
					Parameters: json.RawMessage(`{"apiKey":"embedding-key","embeddingModel":"text-embedding-3-small"}`),
				},
			},
		}
	}

	t.Run("embedding provider placeholder resolves and keeps other parameters", func(t *testing.T) {
		stored := storedConfig()
		incoming := *stored.Clone()
		incoming.EmbeddingSearchConfig.EmbeddingProvider.Parameters = json.RawMessage(
			`{"apiKey":"` + SecretPlaceholder + `","embeddingModel":"text-embedding-3-large"}`)

		restored := RestoreSecrets(incoming, stored)

		params := map[string]string{}
		require.NoError(t, json.Unmarshal(restored.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
		assert.Equal(t, "embedding-key", params["apiKey"])
		assert.Equal(t, "text-embedding-3-large", params["embeddingModel"])
	})

	t.Run("placeholder without a counterpart stays a mask", func(t *testing.T) {
		incoming := Config{
			Services: []llm.ServiceConfig{{ID: "svc-new", APIKey: SecretPlaceholder}},
			MCP: MCPConfig{Servers: []MCPServerConfig{{
				Name:         "Confluence",
				ClientSecret: SecretPlaceholder,
				Headers:      map[string]string{"Authorization": SecretPlaceholder},
			}}},
			WebSearch: WebSearchConfig{Google: WebSearchGoogleConfig{APIKey: SecretPlaceholder}},
			EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
				EmbeddingProvider: embeddings.UpstreamConfig{
					Parameters: json.RawMessage(`{"apiKey":"` + SecretPlaceholder + `"}`),
				},
			},
		}

		restored := RestoreSecrets(incoming, nil)

		assert.Equal(t, SecretPlaceholder, restored.Services[0].APIKey)
		assert.Equal(t, SecretPlaceholder, restored.MCP.Servers[0].ClientSecret)
		assert.Equal(t, SecretPlaceholder, restored.MCP.Servers[0].Headers["Authorization"])
		assert.Equal(t, SecretPlaceholder, restored.WebSearch.Google.APIKey)

		params := map[string]string{}
		require.NoError(t, json.Unmarshal(restored.EmbeddingSearchConfig.EmbeddingProvider.Parameters, &params))
		assert.Equal(t, SecretPlaceholder, params["apiKey"])

		assert.ElementsMatch(t, []string{
			"services[svc-new].apiKey",
			"mcp.servers[Confluence].clientSecret",
			"mcp.servers[Confluence].headers[Authorization]",
			"webSearch.google.apiKey",
			"embeddingSearchConfig.embeddingProvider.parameters.apiKey",
		}, MaskedCredentialFields(restored))
	})

	t.Run("incoming configuration is untouched", func(t *testing.T) {
		stored := storedConfig()
		incoming := *stored.Clone()
		incoming.Services[0].APIKey = SecretPlaceholder

		RestoreSecrets(incoming, stored)

		assert.Equal(t, SecretPlaceholder, incoming.Services[0].APIKey)
	})
}

// TestRestoreSecretsFollowsMCPServerEdits covers the edits the admin console
// makes to the MCP server list. The console edits an entry in place and always
// submits the whole list, so a rename, a URL change, a reorder and a deletion
// all arrive as a list whose masked entries must resolve to the credential of
// the entry the admin was editing — and to no other. An entry edited to a new
// URL keeps its credentials.
func TestRestoreSecretsFollowsMCPServerEdits(t *testing.T) {
	const (
		firstURL     = "https://jira.example.com/mcp"
		secondURL    = "https://confluence.example.com/mcp"
		thirdURL     = "https://github.example.com/mcp"
		firstSecret  = "jira-client-secret"       // #nosec G101 -- test fixture value
		secondSecret = "confluence-client-secret" // #nosec G101 -- test fixture value
		firstHeader  = "Bearer jira-header-token" // #nosec G101 -- test fixture value
		secondHeader = "Bearer conf-header-token" // #nosec G101 -- test fixture value
		headerKey    = "Authorization"
		renamedKey   = "X-Api-Key"
	)

	// storedServers is the persisted list every case starts from.
	storedServers := func() []MCPServerConfig {
		return []MCPServerConfig{
			{
				Name:         "Jira",
				BaseURL:      firstURL,
				ClientSecret: firstSecret,
				Headers:      map[string]string{headerKey: firstHeader},
			},
			{
				Name:         "Confluence",
				BaseURL:      secondURL,
				ClientSecret: secondSecret,
				Headers:      map[string]string{headerKey: secondHeader},
			},
		}
	}

	// masked is what the console holds for a server whose credentials the admin
	// did not touch: the read response, with identifiers still readable.
	masked := func(name, baseURL string, headerKeys ...string) MCPServerConfig {
		server := MCPServerConfig{
			Name:         name,
			BaseURL:      baseURL,
			ClientSecret: SecretPlaceholder,
			Headers:      map[string]string{},
		}
		for _, key := range headerKeys {
			server.Headers[key] = SecretPlaceholder
		}
		return server
	}

	tests := []struct {
		name     string
		incoming []MCPServerConfig
		// want is the credential expected for each incoming entry, by position.
		wantClientSecrets []string
		wantHeaders       []map[string]string
	}{
		{
			name:              "renamed server keeps its credentials",
			incoming:          []MCPServerConfig{masked("Atlassian Jira", firstURL, headerKey), masked("Confluence", secondURL, headerKey)},
			wantClientSecrets: []string{firstSecret, secondSecret},
			wantHeaders:       []map[string]string{{headerKey: firstHeader}, {headerKey: secondHeader}},
		},
		{
			name:              "renamed header key keeps its value",
			incoming:          []MCPServerConfig{masked("Jira", firstURL, renamedKey), masked("Confluence", secondURL, headerKey)},
			wantClientSecrets: []string{firstSecret, secondSecret},
			wantHeaders:       []map[string]string{{renamedKey: firstHeader}, {headerKey: secondHeader}},
		},
		{
			name:              "server pointed at a new url keeps its credentials",
			incoming:          []MCPServerConfig{masked("Jira", thirdURL, headerKey), masked("Confluence", secondURL, headerKey)},
			wantClientSecrets: []string{firstSecret, secondSecret},
			wantHeaders:       []map[string]string{{headerKey: firstHeader}, {headerKey: secondHeader}},
		},
		{
			name:              "reordered servers keep their own credentials",
			incoming:          []MCPServerConfig{masked("Confluence", secondURL, headerKey), masked("Jira", firstURL, headerKey)},
			wantClientSecrets: []string{secondSecret, firstSecret},
			wantHeaders:       []map[string]string{{headerKey: secondHeader}, {headerKey: firstHeader}},
		},
		{
			name:              "deleted server does not hand its credentials to the survivor",
			incoming:          []MCPServerConfig{masked("Confluence", secondURL, headerKey)},
			wantClientSecrets: []string{secondSecret},
			wantHeaders:       []map[string]string{{headerKey: secondHeader}},
		},
		{
			name:              "added server keeps the mask",
			incoming:          []MCPServerConfig{masked("Jira", firstURL, headerKey), masked("Confluence", secondURL, headerKey), masked("GitHub", thirdURL, headerKey)},
			wantClientSecrets: []string{firstSecret, secondSecret, SecretPlaceholder},
			wantHeaders:       []map[string]string{{headerKey: firstHeader}, {headerKey: secondHeader}, {headerKey: SecretPlaceholder}},
		},
		{
			name:              "second server sharing a name keeps the mask",
			incoming:          []MCPServerConfig{masked("Jira", firstURL, headerKey), masked("Jira", thirdURL, headerKey)},
			wantClientSecrets: []string{firstSecret, SecretPlaceholder},
			wantHeaders:       []map[string]string{{headerKey: firstHeader}, {headerKey: SecretPlaceholder}},
		},
		{
			name:              "server renamed and pointed elsewhere at once keeps the mask",
			incoming:          []MCPServerConfig{masked("Something Else", thirdURL, headerKey), masked("Confluence", secondURL, headerKey)},
			wantClientSecrets: []string{SecretPlaceholder, secondSecret},
			wantHeaders:       []map[string]string{{headerKey: SecretPlaceholder}, {headerKey: secondHeader}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := &Config{MCP: MCPConfig{Servers: storedServers()}}
			incoming := Config{MCP: MCPConfig{Servers: tt.incoming}}

			restored := RestoreSecrets(incoming, stored)

			require.Len(t, restored.MCP.Servers, len(tt.incoming))
			for i := range restored.MCP.Servers {
				server := restored.MCP.Servers[i]
				assert.Equal(t, tt.wantClientSecrets[i], server.ClientSecret,
					"client secret of servers[%d] (%q)", i, server.Name)
				assert.Equal(t, tt.wantHeaders[i], server.Headers,
					"headers of servers[%d] (%q)", i, server.Name)
			}
		})
	}
}
