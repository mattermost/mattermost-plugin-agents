// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config_migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	// MigrationKeyServicesToBots is the KV store key for the services to bots migration.
	MigrationKeyServicesToBots = "migration_services_to_bots_2"
)

// BotMigrationConfig represents the old plugin configuration format
// used before the migration to the new bot-based configuration.
type BotMigrationConfig struct {
	Config struct {
		Services []struct {
			Name         string `json:"name"`
			ServiceName  string `json:"serviceName"`
			DefaultModel string `json:"defaultModel"`
			OrgID        string `json:"orgId"`
			URL          string `json:"url"`
			APIKey       string `json:"apiKey"`
			TokenLimit   int    `json:"tokenLimit"`
		} `json:"services"`
	} `json:"config"`
}

// ServicesToBotsMigration migrates the old services array to the new bots configuration.
type ServicesToBotsMigration struct{}

// Key returns the KV store key for this migration.
func (m *ServicesToBotsMigration) Key() string {
	return MigrationKeyServicesToBots
}

// Run executes the migration from services to bots.
func (m *ServicesToBotsMigration) Run(pluginAPI *pluginapi.Client, cfg config.Config) (bool, config.Config, error) {
	pluginAPI.Log.Debug("Checking if migration from services to bots is needed",
		"numBots", len(cfg.Bots),
		"numServices", len(cfg.Services),
	)

	existingConfig := cfg.Clone()

	// If bots already exist, no migration needed
	if len(existingConfig.Bots) != 0 {
		pluginAPI.Log.Debug("Bots already exist, skipping services to bots migration")
		return false, cfg, nil
	}

	pluginAPI.Log.Debug("Migrating services to bots")

	oldConfig := BotMigrationConfig{}
	err := pluginAPI.Configuration.LoadPluginConfiguration(&oldConfig)
	if err != nil {
		return false, cfg, fmt.Errorf("failed to load plugin configuration for migration: %w", err)
	}

	// Create services first
	existingConfig.Services = make([]llm.ServiceConfig, 0, len(oldConfig.Config.Services))
	for _, service := range oldConfig.Config.Services {
		existingConfig.Services = append(existingConfig.Services, llm.ServiceConfig{
			ID:              uuid.New().String(),
			Name:            service.Name,
			Type:            service.ServiceName,
			DefaultModel:    service.DefaultModel,
			OrgID:           service.OrgID,
			APIURL:          service.URL,
			APIKey:          service.APIKey,
			InputTokenLimit: service.TokenLimit,
		})
	}

	// Create bots that reference the services
	existingConfig.Bots = make([]llm.BotConfig, 0, len(existingConfig.Services))
	for i, service := range existingConfig.Services {
		botID := uuid.New().String()
		botName := fmt.Sprintf("ai%d", i+1)
		displayName := service.Name
		existingConfig.Bots = append(existingConfig.Bots, llm.BotConfig{
			ID:          botID,
			Name:        botName,
			DisplayName: displayName,
			ServiceID:   service.ID,
		})
	}

	return true, *existingConfig, nil
}
