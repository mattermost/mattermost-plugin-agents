// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// LegacyServiceConfig represents the old config.json format with the legacy service fields.
// This is used during the one-time migration from config.json to the database.
type LegacyServiceConfig struct {
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

// MigrateServicesToBots migrates legacy service configs (from old config.json format)
// to the new bots+services model. Returns the updated config, whether any changes were made,
// and any error.
//
// loadLegacyConfig is a function that loads the legacy JSON configuration (e.g., from
// config.json via the plugin API). It is only called if migration appears necessary.
func MigrateServicesToBots(cfg Config, loadLegacyConfig func() (LegacyServiceConfig, error)) (Config, bool, error) {
	existingConfig := cfg.Clone()

	// If bots already exist, no migration needed
	if len(existingConfig.Bots) != 0 {
		return cfg, false, nil
	}

	// If services already use the new schema (type or id set), no migration needed.
	// This keeps the "services but no bots" state valid in the new config format.
	for _, service := range existingConfig.Services {
		if service.ID != "" || service.Type != "" {
			return cfg, false, nil
		}
	}

	oldConfig, err := loadLegacyConfig()
	if err != nil {
		return cfg, false, fmt.Errorf("failed to load legacy config for migration: %w", err)
	}

	// If there are no old services to migrate either, nothing to do
	if len(oldConfig.Config.Services) == 0 {
		return cfg, false, nil
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
		return cfg, false, nil
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

	return *existingConfig, true, nil
}

// MigrateSeparateServicesFromBots extracts embedded service configs from bots into
// standalone service entries, deduplicating identical services. Returns the updated
// config, whether any changes were made, and any error.
func MigrateSeparateServicesFromBots(cfg Config) (Config, bool, error) {
	existingConfig := cfg.Clone()

	// If no bots, nothing to migrate
	if len(existingConfig.Bots) == 0 {
		return cfg, false, nil
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
		return cfg, false, nil
	}

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

	return *existingConfig, true, nil
}

// RunAllLegacyMigrations runs all legacy config migrations in order.
// Returns the final config, whether any changes were made, and any error.
//
// loadLegacyConfig is passed through to MigrateServicesToBots; it is only called
// if that specific migration is needed.
func RunAllLegacyMigrations(cfg Config, loadLegacyConfig func() (LegacyServiceConfig, error)) (Config, bool, error) {
	changed := false

	newCfg, didChange, err := MigrateServicesToBots(cfg, loadLegacyConfig)
	if err != nil {
		return cfg, false, fmt.Errorf("failed to migrate services to bots: %w", err)
	}
	if didChange {
		changed = true
		cfg = newCfg
	}

	newCfg, didChange, err = MigrateSeparateServicesFromBots(cfg)
	if err != nil {
		return cfg, false, fmt.Errorf("failed to migrate separate services from bots: %w", err)
	}
	if didChange {
		changed = true
		cfg = newCfg
	}

	return cfg, changed, nil
}

func generateServiceID() string {
	return uuid.New().String()
}

// findIdenticalService checks if a service with identical configuration already exists.
func findIdenticalService(serviceMap map[string]llm.ServiceConfig, newSvc *llm.ServiceConfig) string {
	for id, existingSvc := range serviceMap {
		if servicesAreIdentical(existingSvc, *newSvc) {
			return id
		}
	}
	return ""
}

// servicesAreIdentical compares all fields of two ServiceConfigs (excluding ID and Name).
// Name is excluded because it's a display label - services with identical configuration
// but different names should be deduplicated.
func servicesAreIdentical(a, b llm.ServiceConfig) bool {
	if a.Type != b.Type ||
		a.APIKey != b.APIKey ||
		a.OrgID != b.OrgID ||
		a.DefaultModel != b.DefaultModel ||
		a.APIURL != b.APIURL ||
		a.InputTokenLimit != b.InputTokenLimit ||
		a.StreamingTimeoutSeconds != b.StreamingTimeoutSeconds ||
		a.OutputTokenLimit != b.OutputTokenLimit ||
		a.UseResponsesAPI != b.UseResponsesAPI {
		return false
	}
	return true
}
