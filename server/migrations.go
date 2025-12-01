// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

const (
	MigrationKeyServicesToBots           = "migration_services_to_bots_2"
	MigrationKeySeparateServicesFromBots = "migration_separate_services_from_bots"
)

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

func migrateSeparateServicesFromBots(pluginAPI *pluginapi.Client, cfg config.Config) (bool, config.Config, error) {
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
		serviceID := generateServiceID()

		// Check if similar service already exists (deduplication)
		existingID := findIdenticalService(serviceMap, bot.Service)
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

func generateServiceID() string {
	return uuid.New().String()
}

// Helper to find if similar service already exists
func findIdenticalService(serviceMap map[string]llm.ServiceConfig, newSvc *llm.ServiceConfig) string {
	for id, existingSvc := range serviceMap {
		if servicesAreIdentical(existingSvc, *newSvc) {
			return id
		}
	}
	return ""
}

// servicesAreIdentical compares all fields of two ServiceConfigs (excluding ID and Name)
// Name is excluded because it's a display label - services with identical configuration
// but different names should be deduplicated.
func servicesAreIdentical(a, b llm.ServiceConfig) bool {
	// Compare all scalar fields except Name (which is a display label)
	if a.Type != b.Type ||
		a.APIKey != b.APIKey ||
		a.OrgID != b.OrgID ||
		a.DefaultModel != b.DefaultModel ||
		a.APIURL != b.APIURL ||
		a.InputTokenLimit != b.InputTokenLimit ||
		a.StreamingTimeoutSeconds != b.StreamingTimeoutSeconds ||
		a.SendUserID != b.SendUserID ||
		a.OutputTokenLimit != b.OutputTokenLimit ||
		a.UseResponsesAPI != b.UseResponsesAPI {
		return false
	}
	return true
}

func migrateServicesToBots(pluginAPI *pluginapi.Client, cfg config.Config) (bool, config.Config, error) {
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

// runAllMigrations executes all migrations under a single mutex to prevent race conditions
// in multi-instance deployments. Persists the updated configuration and marks migrations as
// complete only after successful save. Returns the final configuration and any errors encountered.
func runAllMigrations(mutexAPI cluster.MutexPluginAPI, pluginAPI *pluginapi.Client, cfg config.Config) (config.Config, bool, error) {
	mtx, err := cluster.NewMutex(mutexAPI, "ai_all_migrations")
	if err != nil {
		return config.Config{}, false, fmt.Errorf("failed to create migrations mutex: %w", err)
	}
	mtx.Lock()

	changed := false

	var kvVal []byte

	// Check Migration 1: Services to Bots
	migratedServicesToBots := false
	if err := pluginAPI.KV.Get(MigrationKeyServicesToBots, &kvVal); err != nil || kvVal == nil {
		didMigrateServicesToBots, newCfg, migrateErr := migrateServicesToBots(pluginAPI, cfg)
		if migrateErr != nil {
			mtx.Unlock()
			return cfg, false, fmt.Errorf("failed to migrate services to bots: %w", migrateErr)
		}
		if didMigrateServicesToBots {
			changed = true
			migratedServicesToBots = true
			cfg = newCfg
			pluginAPI.Log.Info("Migration completed: services to bots")
		}
	}

	// Check Migration 2: Separate Services from Bots
	migratedSeparateServicesFromBots := false
	if err := pluginAPI.KV.Get(MigrationKeySeparateServicesFromBots, &kvVal); err != nil || kvVal == nil {
		didMigrateSeparateServicesFromBots, newCfg, migrateErr := migrateSeparateServicesFromBots(pluginAPI, cfg)
		if migrateErr != nil {
			mtx.Unlock()
			return cfg, false, fmt.Errorf("failed to migrate separate services from bots: %w", migrateErr)
		}
		if didMigrateSeparateServicesFromBots {
			changed = true
			migratedSeparateServicesFromBots = true
			cfg = newCfg
			pluginAPI.Log.Info("Migration completed: separate services from bots")
		}
	}

	if changed {
		// Prepare config to save before unlocking
		wrappedConfig := configuration{Config: cfg}

		// Convert config to map[string]any for plugin API
		out := map[string]any{}
		marshalBytes, marshalErr := json.Marshal(wrappedConfig)
		if marshalErr != nil {
			mtx.Unlock()
			return cfg, false, fmt.Errorf("failed to marshal migrated configuration: %w", marshalErr)
		}
		if unmarshalErr := json.Unmarshal(marshalBytes, &out); unmarshalErr != nil {
			mtx.Unlock()
			return cfg, false, fmt.Errorf("failed to unmarshal migrated configuration: %w", unmarshalErr)
		}

		// Mark migrations as completed in KV store BEFORE unlocking to prevent race conditions.
		// If the save fails later, we will revert these keys.
		if migratedServicesToBots {
			if _, err := pluginAPI.KV.Set(MigrationKeyServicesToBots, []byte("true")); err != nil {
				pluginAPI.Log.Warn("Failed to mark services to bots migration as completed in KV store", "error", err)
			}
		}
		if migratedSeparateServicesFromBots {
			if _, err := pluginAPI.KV.Set(MigrationKeySeparateServicesFromBots, []byte("true")); err != nil {
				pluginAPI.Log.Warn("Failed to mark separate services from bots migration as completed in KV store", "error", err)
			}
		}

		// Release mutex before saving config to avoid deadlock when SavePluginConfig
		// triggers OnConfigurationChange which tries to acquire the same mutex
		mtx.Unlock()

		if saveErr := pluginAPI.Configuration.SavePluginConfig(out); saveErr != nil {
			// Revert KV keys if save failed, so we retry next time
			mtx.Lock()
			if migratedServicesToBots {
				if err := pluginAPI.KV.Delete(MigrationKeyServicesToBots); err != nil {
					pluginAPI.Log.Warn("Failed to revert services to bots migration KV key", "error", err)
				}
			}
			if migratedSeparateServicesFromBots {
				if err := pluginAPI.KV.Delete(MigrationKeySeparateServicesFromBots); err != nil {
					pluginAPI.Log.Warn("Failed to revert separate services from bots migration KV key", "error", err)
				}
			}
			mtx.Unlock()

			return cfg, false, fmt.Errorf("failed to save migrated configuration: %w", saveErr)
		}

		pluginAPI.Log.Info("Configuration persisted after migrations")
	} else {
		mtx.Unlock()
	}

	return cfg, changed, nil
}
