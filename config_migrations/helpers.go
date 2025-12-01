// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config_migrations

import (
	"github.com/google/uuid"
	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// GenerateServiceID generates a new unique service ID.
func GenerateServiceID() string {
	return uuid.New().String()
}

// FindIdenticalService finds if a similar service already exists in the service map.
// Returns the ID of the matching service, or empty string if no match found.
func FindIdenticalService(serviceMap map[string]llm.ServiceConfig, newSvc *llm.ServiceConfig) string {
	for id, existingSvc := range serviceMap {
		if ServicesAreIdentical(existingSvc, *newSvc) {
			return id
		}
	}
	return ""
}

// ServicesAreIdentical compares all fields of two ServiceConfigs (excluding ID and Name).
// Name is excluded because it's a display label - services with identical configuration
// but different names should be deduplicated.
func ServicesAreIdentical(a, b llm.ServiceConfig) bool {
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
