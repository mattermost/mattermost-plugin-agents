// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"path/filepath"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/telemetry"
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

	body, marshalErr := yaml.Marshal(packet)
	if marshalErr != nil {
		return nil, errors.Wrap(marshalErr, "failed to marshal diagnostics")
	}

	return []*model.FileData{{
		Filename: filepath.Join(manifest.Id, "diagnostics.yaml"),
		Body:     body,
	}}, err
}

// buildSupportPacket assembles the diagnostics struct from config and the store.
// It returns a partially-populated packet alongside any non-fatal errors.
func buildSupportPacket(store agentCounter, cfg *config.Config, version string) (*SupportPacket, error) {
	var result *multierror.Error

	var totalAgents *int
	agentCount, err := store.CountActiveAgents()
	if err != nil {
		result = multierror.Append(result, errors.Wrap(err, "failed to get agent count for Support Packet"))
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
		AllowNativeWebSearchInChannels:  cfg.AllowNativeWebSearchInChannels,
		WebSearchEnabled:                cfg.WebSearch.Enabled,
		EmbeddingSearchEnabled:          cfg.EmbeddingSearchConfig.Type != "",
		TelemetryEnabled:                telemetryMode != "" && telemetryMode != telemetry.OutputModeOff,
	}, result.ErrorOrNil()
}
