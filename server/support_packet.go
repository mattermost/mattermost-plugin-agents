// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// agentCounter is the subset of the store used by support packet generation.
type agentCounter interface {
	CountActiveAgents() (int, error)
}

// SupportPacket contains diagnostics data included in the Mattermost Support Packet.
type SupportPacket struct {
	Version string `yaml:"version"`

	// Agent counts — omitted (not printed) when the DB query fails.
	TotalAgents *int `yaml:"total_agents,omitempty"`

	// LLM service configuration (no secrets)
	TotalLLMServices int      `yaml:"total_llm_services"`
	LLMServiceTypes  []string `yaml:"llm_service_types"`

	// MCP configuration
	MCPEnabled        bool `yaml:"mcp_enabled"`
	TotalMCPServers   int  `yaml:"total_mcp_servers"`
	EnabledMCPServers int  `yaml:"enabled_mcp_servers"`

	// Feature flags
	EnableCallSummary               bool `yaml:"enable_call_summary"`
	EnableTokenUsageLogging         bool `yaml:"enable_token_usage_logging"`
	EnableChannelMentionToolCalling bool `yaml:"enable_channel_mention_tool_calling"`
	EnableAmbientAutoReply          bool `yaml:"enable_ambient_auto_reply"`
	AllowNativeWebSearchInChannels  bool `yaml:"allow_native_web_search_in_channels"`
	WebSearchEnabled                bool `yaml:"web_search_enabled"`
	EmbeddingSearchEnabled          bool `yaml:"embedding_search_enabled"`
	TelemetryEnabled                bool `yaml:"telemetry_enabled"`
}

func (p *Plugin) GenerateSupportData(_ *plugin.Context) ([]*model.FileData, error) {
	packet, err := buildSupportPacket(p.store, p.configuration.Config(), manifest.Version)
	if err != nil {
		// Non-fatal: return whatever partial data we have.
		p.pluginAPI.Log.Warn("Support packet generated with errors", "error", err)
	}

	return []*model.FileData{{
		Filename: filepath.Join(manifest.Id, "diagnostics.yaml"),
		Body:     packet.encodeYAML(),
	}}, err
}

func (s *SupportPacket) encodeYAML() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "version: %s\n", yamlScalar(s.Version))
	if s.TotalAgents != nil {
		fmt.Fprintf(&b, "total_agents: %d\n", *s.TotalAgents)
	}
	fmt.Fprintf(&b, "total_llm_services: %d\n", s.TotalLLMServices)
	if len(s.LLMServiceTypes) == 0 {
		b.WriteString("llm_service_types: []\n")
	} else {
		b.WriteString("llm_service_types:\n")
		for _, svcType := range s.LLMServiceTypes {
			fmt.Fprintf(&b, "    - %s\n", yamlScalar(svcType))
		}
	}
	fmt.Fprintf(&b, "mcp_enabled: %t\n", s.MCPEnabled)
	fmt.Fprintf(&b, "total_mcp_servers: %d\n", s.TotalMCPServers)
	fmt.Fprintf(&b, "enabled_mcp_servers: %d\n", s.EnabledMCPServers)
	fmt.Fprintf(&b, "enable_call_summary: %t\n", s.EnableCallSummary)
	fmt.Fprintf(&b, "enable_token_usage_logging: %t\n", s.EnableTokenUsageLogging)
	fmt.Fprintf(&b, "enable_channel_mention_tool_calling: %t\n", s.EnableChannelMentionToolCalling)
	fmt.Fprintf(&b, "enable_ambient_auto_reply: %t\n", s.EnableAmbientAutoReply)
	fmt.Fprintf(&b, "allow_native_web_search_in_channels: %t\n", s.AllowNativeWebSearchInChannels)
	fmt.Fprintf(&b, "web_search_enabled: %t\n", s.WebSearchEnabled)
	fmt.Fprintf(&b, "embedding_search_enabled: %t\n", s.EmbeddingSearchEnabled)
	fmt.Fprintf(&b, "telemetry_enabled: %t\n", s.TelemetryEnabled)
	return []byte(b.String())
}

func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	// Quote anything outside a conservative plain-scalar alphabet so YAML
	// parsers never misread booleans, numbers, comments, or special characters.
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		return strconv.Quote(s)
	}
	for i := range len(s) {
		if !isPlainYAMLByte(s[i]) {
			return strconv.Quote(s)
		}
	}
	if s[0] == '-' || s[0] == '.' {
		return strconv.Quote(s)
	}
	return s
}

func isPlainYAMLByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_'
}

// buildSupportPacket assembles the diagnostics struct from config and the store.
// It returns a partially-populated packet alongside any non-fatal errors.
func buildSupportPacket(store agentCounter, cfg *config.Config, version string) (*SupportPacket, error) {
	var totalAgents *int
	agentCount, err := store.CountActiveAgents()
	if err != nil {
		err = fmt.Errorf("failed to get agent count for Support Packet: %w", err)
	} else {
		totalAgents = &agentCount
	}

	serviceTypes := make([]string, 0, len(cfg.Services))
	for _, svc := range cfg.Services {
		serviceTypes = append(serviceTypes, svc.Type)
	}

	enabledMCPServers := 0
	for _, s := range cfg.MCP.Servers {
		if s.Enabled {
			enabledMCPServers++
		}
	}

	telemetryMode := telemetry.OutputMode(cfg.TelemetryOutput)

	return &SupportPacket{
		Version: version,

		TotalAgents: totalAgents,

		TotalLLMServices: len(cfg.Services),
		LLMServiceTypes:  serviceTypes,

		MCPEnabled:        cfg.MCP.Enabled,
		TotalMCPServers:   len(cfg.MCP.Servers),
		EnabledMCPServers: enabledMCPServers,

		EnableCallSummary:               cfg.EnableCallSummary,
		EnableTokenUsageLogging:         cfg.EnableTokenUsageLogging,
		EnableChannelMentionToolCalling: cfg.EnableChannelMentionToolCalling,
		EnableAmbientAutoReply:          cfg.EnableAmbientAutoReply,
		AllowNativeWebSearchInChannels:  cfg.AllowNativeWebSearchInChannels,
		WebSearchEnabled:                cfg.WebSearch.Enabled,
		EmbeddingSearchEnabled:          cfg.EmbeddingSearchConfig.Type != "",
		TelemetryEnabled:                telemetryMode != "" && telemetryMode != telemetry.OutputModeOff,
	}, err
}
