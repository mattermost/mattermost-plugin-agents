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
		if bot.Service != nil && bot.Service.Type != "" && bot.ServiceID == "" {
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
	pluginAPI.Log.Debug("Checking if migration from services to bots is needed")

	existingConfig := cfg.Clone()

	// If bots already exist, no migration needed
	if len(existingConfig.Bots) != 0 {
		return false, cfg, nil
	}

	// If services already use the new schema (type or id set), no migration needed.
	// This keeps the "services but no bots" state valid in the new config format.
	for _, service := range existingConfig.Services {
		if service.ID != "" || service.Type != "" {
			return false, cfg, nil
		}
	}

	oldConfig := BotMigrationConfig{}
	err := pluginAPI.Configuration.LoadPluginConfiguration(&oldConfig)
	if err != nil {
		return false, cfg, fmt.Errorf("failed to load plugin configuration for migration: %w", err)
	}

	// If there are no old services to migrate either, nothing to do
	if len(oldConfig.Config.Services) == 0 {
		return false, cfg, nil
	}

	// Only migrate legacy service configs that include legacy fields.
	hasLegacyServiceFields := false
	for _, service := range oldConfig.Config.Services {
		if service.ServiceName != "" || service.URL != "" {
			hasLegacyServiceFields = true
			break
		}
	}
	if !hasLegacyServiceFields {
		return false, cfg, nil
	}

	pluginAPI.Log.Debug("Migrating services to bots")

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
	// Optimistic check: immediately run the migration to determine if it's actually needed, return early to avoid acquiring the cluster mutex.

	servicesToBotsNeeded, _, err := migrateServicesToBots(pluginAPI, cfg)
	if err != nil {
		return cfg, false, fmt.Errorf("failed to check services to bots migration: %w", err)
	}

	separateServicesFromBotsNeeded, _, err := migrateSeparateServicesFromBots(pluginAPI, cfg)
	if err != nil {
		return cfg, false, fmt.Errorf("failed to check separate services from bots migration: %w", err)
	}

	if !servicesToBotsNeeded && !separateServicesFromBotsNeeded {
		return cfg, false, nil
	}

	mtx, err := cluster.NewMutex(mutexAPI, "ai_all_migrations")
	if err != nil {
		return config.Config{}, false, fmt.Errorf("failed to create migrations mutex: %w", err)
	}
	mtx.Lock()

	// Reload configuration inside lock to ensure we have the latest version
	// This handles the race condition where another node might have finished migration
	// while we were waiting for the lock.
	latestConfigWrap := new(configuration)
	if lErr := pluginAPI.Configuration.LoadPluginConfiguration(latestConfigWrap); lErr != nil {
		mtx.Unlock()
		return cfg, false, fmt.Errorf("failed to reload configuration inside lock: %w", lErr)
	}
	cfg = latestConfigWrap.Config

	changed := false

	didMigrateServicesToBots, newCfg, err := migrateServicesToBots(pluginAPI, cfg)
	if err != nil {
		mtx.Unlock()
		return cfg, false, fmt.Errorf("failed to migrate services to bots: %w", err)
	}
	if didMigrateServicesToBots {
		changed = true
		cfg = newCfg
		pluginAPI.Log.Info("Migration completed: services to bots")
	}

	var migrateErr error
	didMigrateSeparateServicesFromBots := false
	didMigrateSeparateServicesFromBots, newCfg, migrateErr = migrateSeparateServicesFromBots(pluginAPI, cfg)
	if migrateErr != nil {
		mtx.Unlock()
		return cfg, false, fmt.Errorf("failed to migrate separate services from bots: %w", migrateErr)
	}
	if didMigrateSeparateServicesFromBots {
		changed = true
		cfg = newCfg
		pluginAPI.Log.Info("Migration completed: separate services from bots")
	}

	// Release mutex before saving config to avoid deadlock when SavePluginConfig
	// triggers OnConfigurationChange which tries to acquire the same mutex
	mtx.Unlock()

	// If any migrations ran, persist the config
	if changed {
		// Wrap config in the configuration struct that has the proper nesting
		wrappedConfig := configuration{Config: cfg}

		// Convert config to map[string]any for plugin API
		out := map[string]any{}
		marshalBytes, marshalErr := json.Marshal(wrappedConfig)
		if marshalErr != nil {
			return cfg, false, fmt.Errorf("failed to marshal migrated configuration: %w", marshalErr)
		}
		if unmarshalErr := json.Unmarshal(marshalBytes, &out); unmarshalErr != nil {
			return cfg, false, fmt.Errorf("failed to unmarshal migrated configuration: %w", unmarshalErr)
		}

		if saveErr := pluginAPI.Configuration.SavePluginConfig(out); saveErr != nil {
			return cfg, false, fmt.Errorf("failed to save migrated configuration: %w", saveErr)
		}

		pluginAPI.Log.Info("Configuration persisted after migrations")
	}

	return cfg, changed, nil
}
