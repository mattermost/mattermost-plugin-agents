// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config_migrations

import (
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	// MigrationKeySeparateServicesFromBots is the KV store key for the separate services migration.
	MigrationKeySeparateServicesFromBots = "migration_separate_services_from_bots"
)

// SeparateServicesFromBotsMigration extracts embedded services from bots into separate service configs.
type SeparateServicesFromBotsMigration struct{}

// Key returns the KV store key for this migration.
func (m *SeparateServicesFromBotsMigration) Key() string {
	return MigrationKeySeparateServicesFromBots
}

// Run executes the migration to separate services from bots.
func (m *SeparateServicesFromBotsMigration) Run(pluginAPI *pluginapi.Client, cfg config.Config) (bool, config.Config, error) {
	pluginAPI.Log.Debug("Checking if migration to separate services from bots is needed")

	existingConfig := cfg.Clone()

	// If no bots, nothing to migrate
	if len(existingConfig.Bots) == 0 {
		return false, cfg, nil
	}

	// Check if migration is needed - if any bot has embedded service
	needsMigration := false
	for _, bot := range existingConfig.Bots {
		pluginAPI.Log.Debug("Checking bot for migration",
			"botID", bot.ID,
			"hasService", bot.Service != nil,
			"serviceID", bot.ServiceID,
		)
		if bot.Service != nil && bot.Service.Type != "" && bot.ServiceID == "" {
			pluginAPI.Log.Debug("Bot needs migration",
				"botID", bot.ID,
				"serviceType", bot.Service.Type,
			)
			needsMigration = true
			break
		}
	}

	if !needsMigration {
		pluginAPI.Log.Debug("No migration needed - bots already use service references")
		return false, cfg, nil
	}

	pluginAPI.Log.Info("Migrating to separate services from bots")

	// Extract and deduplicate services
	// Initialize serviceMap with existing services so we can deduplicate against them
	serviceMap := make(map[string]llm.ServiceConfig)
	for _, svc := range existingConfig.Services {
		serviceMap[svc.ID] = svc
	}
	botServiceMapping := make(map[string]string)

	for _, bot := range existingConfig.Bots {
		// Skip if already migrated (has serviceID)
		if bot.ServiceID != "" {
			botServiceMapping[bot.ID] = bot.ServiceID
			continue
		}

		// Skip if no embedded service
		if bot.Service == nil || bot.Service.Type == "" {
			continue
		}

		// Generate service ID
		serviceID := GenerateServiceID()

		// Check if similar service already exists (deduplication)
		existingID := FindIdenticalService(serviceMap, bot.Service)
		if existingID != "" {
			serviceID = existingID
		} else {
			newService := *bot.Service
			newService.ID = serviceID
			serviceMap[serviceID] = newService
		}

		botServiceMapping[bot.ID] = serviceID
	}

	// Convert service map to array (includes both existing and newly extracted services)
	existingConfig.Services = make([]llm.ServiceConfig, 0, len(serviceMap))
	for _, svc := range serviceMap {
		existingConfig.Services = append(existingConfig.Services, svc)
	}

	// Update bots to reference services by ID and clear embedded service field
	for i := range existingConfig.Bots {
		if serviceID, ok := botServiceMapping[existingConfig.Bots[i].ID]; ok {
			existingConfig.Bots[i].ServiceID = serviceID
			// Clear the embedded service field now that it's been extracted
			existingConfig.Bots[i].Service = nil
		}
	}

	return true, *existingConfig, nil
}
