// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
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

// MigratePluginConfig inspects the plugin settings map and performs in-memory migrations if needed.
// It returns the updated map, a boolean indicating if changes were made, and any error.
func MigratePluginConfig(pluginSettings map[string]any) (map[string]any, bool, error) {
	// Marshal to JSON to work with structs
	data, err := json.Marshal(pluginSettings)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal plugin settings: %w", err)
	}

	// Load into current configuration struct
	var cfg configuration
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal into configuration: %w", err)
	}

	changed := false

	// Migration 1: Services to Bots
	// This migration checks for the legacy "services" array in the config and if bots are missing.
	// Since json tags might differ ("serviceName" vs "type"), we also need to check the legacy struct.
	if len(cfg.Config.Bots) == 0 {
		var oldConfig BotMigrationConfig
		// Try to unmarshal into old config structure to see if we have legacy services
		if err := json.Unmarshal(data, &oldConfig); err == nil && len(oldConfig.Config.Services) > 0 {
			if migrateServicesToBots(&oldConfig, &cfg.Config) {
				changed = true
			}
		}
	}

	// Migration 2: Separate Services from Bots
	// This checks if any bot has an embedded service definition and extracts it.
	if updated, err := migrateSeparateServicesFromBots(&cfg.Config); err != nil {
		return nil, false, fmt.Errorf("failed to migrate separate services from bots: %w", err)
	} else if updated {
		changed = true
	}

	if !changed {
		return pluginSettings, false, nil
	}

	// Marshal back to map
	newData, err := json.Marshal(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal migrated config: %w", err)
	}

	var newSettings map[string]any
	if err := json.Unmarshal(newData, &newSettings); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal migrated config to map: %w", err)
	}

	return newSettings, true, nil
}

func migrateServicesToBots(oldConfig *BotMigrationConfig, existingConfig *config.Config) bool {
	// Safety check: if bots already exist, don't migrate
	if len(existingConfig.Bots) > 0 {
		return false
	}

	// Safety check: if no old services, nothing to migrate
	if len(oldConfig.Config.Services) == 0 {
		return false
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

	return true
}

func migrateSeparateServicesFromBots(existingConfig *config.Config) (bool, error) {
	// If no bots, nothing to migrate
	if len(existingConfig.Bots) == 0 {
		return false, nil
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
		return false, nil
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

	return true, nil
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
