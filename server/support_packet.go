// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"path/filepath"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// SupportPacket contains diagnostics data included in the Mattermost Support Packet.
type SupportPacket struct {
	Version string `yaml:"version"`

	// Agent counts
	TotalAgents int `yaml:"total_agents"`

	// LLM service configuration (no secrets)
	TotalLLMServices      int      `yaml:"total_llm_services"`
	LLMServiceTypes       []string `yaml:"llm_service_types"`

	// MCP configuration
	MCPEnabled        bool `yaml:"mcp_enabled"`
	TotalMCPServers   int  `yaml:"total_mcp_servers"`
	EnabledMCPServers int  `yaml:"enabled_mcp_servers"`

	// Feature flags
	EnableCallSummary               bool `yaml:"enable_call_summary"`
	EnableTokenUsageLogging         bool `yaml:"enable_token_usage_logging"`
	EnableChannelMentionToolCalling bool `yaml:"enable_channel_mention_tool_calling"`
	AllowNativeWebSearchInChannels  bool `yaml:"allow_native_web_search_in_channels"`
	WebSearchEnabled                bool `yaml:"web_search_enabled"`
	EmbeddingSearchEnabled          bool `yaml:"embedding_search_enabled"`
	TelemetryEnabled                bool `yaml:"telemetry_enabled"`
}

func (p *Plugin) GenerateSupportData(_ *plugin.Context) ([]*model.FileData, error) {
	var result *multierror.Error

	cfg := p.configuration.Config()

	agentCount, err := p.store.CountActiveAgents()
	if err != nil {
		result = multierror.Append(result, errors.Wrap(err, "failed to get agent count for Support Packet"))
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

	packet := SupportPacket{
		Version: manifest.Version,

		TotalAgents: agentCount,

		TotalLLMServices:      len(cfg.Services),
		LLMServiceTypes:       serviceTypes,

		MCPEnabled:        cfg.MCP.Enabled,
		TotalMCPServers:   len(cfg.MCP.Servers),
		EnabledMCPServers: enabledMCPServers,

		EnableCallSummary:               cfg.EnableCallSummary,
		EnableTokenUsageLogging:         cfg.EnableTokenUsageLogging,
		EnableChannelMentionToolCalling: cfg.EnableChannelMentionToolCalling,
		AllowNativeWebSearchInChannels:  cfg.AllowNativeWebSearchInChannels,
		WebSearchEnabled:                cfg.WebSearch.Enabled,
		EmbeddingSearchEnabled:          cfg.EmbeddingSearchConfig.Type != "",
		TelemetryEnabled:                cfg.TelemetryOutput != "",
	}

	body, err := yaml.Marshal(packet)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal diagnostics")
	}

	return []*model.FileData{{
		Filename: filepath.Join(manifest.Id, "diagnostics.yaml"),
		Body:     body,
	}}, result.ErrorOrNil()
}
