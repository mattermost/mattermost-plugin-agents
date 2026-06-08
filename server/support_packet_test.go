// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// stubAgentCounter is a minimal agentCounter for support packet tests.
type stubAgentCounter struct {
	count int
	err   error
}

func (s stubAgentCounter) CountActiveAgents() (int, error) { return s.count, s.err }

func fullTestConfig() *config.Config {
	return &config.Config{
		Services: []llm.ServiceConfig{
			{Type: "openai"},
			{Type: "anthropic"},
		},
		MCP: config.MCPConfig{
			Enabled: true,
			Servers: []config.MCPServerConfig{
				{Name: "server-a", Enabled: true},
				{Name: "server-b", Enabled: false},
				{Name: "server-c", Enabled: true},
			},
		},
		EnableCallSummary:               true,
		EnableTokenUsageLogging:         true,
		EnableChannelMentionToolCalling: true,
		AllowNativeWebSearchInChannels:  true,
		WebSearch:                       config.WebSearchConfig{Enabled: true},
		EmbeddingSearchConfig:           embeddings.EmbeddingSearchConfig{Type: "postgres"},
		TelemetryOutput:                 "logs",
	}
}

func TestBuildSupportPacket(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := stubAgentCounter{count: 7}
		cfg := fullTestConfig()

		packet, err := buildSupportPacket(store, cfg, "1.0.0-test")

		require.NoError(t, err)
		require.NotNil(t, packet)

		assert.Equal(t, 7, *packet.TotalAgents)
		assert.Equal(t, 2, packet.TotalLLMServices)
		assert.Equal(t, []string{"openai", "anthropic"}, packet.LLMServiceTypes)
		assert.True(t, packet.MCPEnabled)
		assert.Equal(t, 3, packet.TotalMCPServers)
		assert.Equal(t, 2, packet.EnabledMCPServers)
		assert.True(t, packet.EnableCallSummary)
		assert.True(t, packet.EnableTokenUsageLogging)
		assert.True(t, packet.EnableChannelMentionToolCalling)
		assert.True(t, packet.AllowNativeWebSearchInChannels)
		assert.True(t, packet.WebSearchEnabled)
		assert.True(t, packet.EmbeddingSearchEnabled)
		assert.True(t, packet.TelemetryEnabled)
	})

	t.Run("store error omits agent count but returns other fields", func(t *testing.T) {
		store := stubAgentCounter{err: errors.New("db unavailable")}
		cfg := fullTestConfig()

		packet, err := buildSupportPacket(store, cfg, "1.0.0-test")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db unavailable")

		require.NotNil(t, packet)
		assert.Nil(t, packet.TotalAgents, "TotalAgents must be omitted when the count fails")
		assert.Equal(t, 2, packet.TotalLLMServices)
		assert.True(t, packet.TelemetryEnabled)

		body, marshalErr := yaml.Marshal(packet)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(body), "total_agents")
	})

	t.Run("telemetry enabled flag", func(t *testing.T) {
		tests := []struct {
			name            string
			telemetryOutput string
			wantEnabled     bool
		}{
			{"empty", "", false},
			{"off", "off", false},
			{"logs", "logs", true},
			{"otlp", "otlp", true},
		}

		store := stubAgentCounter{}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg := &config.Config{TelemetryOutput: tt.telemetryOutput}
				packet, err := buildSupportPacket(store, cfg, "1.0.0-test")
				require.NoError(t, err)
				assert.Equal(t, tt.wantEnabled, packet.TelemetryEnabled)
			})
		}
	})

	t.Run("embedding search enabled flag", func(t *testing.T) {
		tests := []struct {
			name          string
			embeddingType string
			wantEnabled   bool
		}{
			{"empty", "", false},
			{"postgres", "postgres", true},
			{"other", "other", true},
		}

		store := stubAgentCounter{}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg := &config.Config{
					EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{Type: tt.embeddingType},
				}
				packet, err := buildSupportPacket(store, cfg, "1.0.0-test")
				require.NoError(t, err)
				assert.Equal(t, tt.wantEnabled, packet.EmbeddingSearchEnabled)
			})
		}
	})

	t.Run("MCP server counting", func(t *testing.T) {
		tests := []struct {
			name        string
			servers     []config.MCPServerConfig
			wantTotal   int
			wantEnabled int
		}{
			{"no servers", nil, 0, 0},
			{"all disabled", []config.MCPServerConfig{{Enabled: false}, {Enabled: false}}, 2, 0},
			{"all enabled", []config.MCPServerConfig{{Enabled: true}, {Enabled: true}}, 2, 2},
			{"mixed", []config.MCPServerConfig{{Enabled: true}, {Enabled: false}, {Enabled: true}}, 3, 2},
		}

		store := stubAgentCounter{}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg := &config.Config{MCP: config.MCPConfig{Servers: tt.servers}}
				packet, err := buildSupportPacket(store, cfg, "1.0.0-test")
				require.NoError(t, err)
				assert.Equal(t, tt.wantTotal, packet.TotalMCPServers)
				assert.Equal(t, tt.wantEnabled, packet.EnabledMCPServers)
			})
		}
	})
}
