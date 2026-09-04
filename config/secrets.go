// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"bytes"
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// FakeSetting is returned in place of a configured credential.
const FakeSetting = model.FakeSetting

var embeddingCredentialFields = []string{
	"apiKey",
	"awsSecretAccessKey",
	"vertexAuthCredentials",
}

// RedactSecrets returns a copy of cfg with configured credentials replaced by
// FakeSetting. Empty credential fields remain empty.
func RedactSecrets(cfg Config) Config {
	// A malformed raw provider configuration cannot be deep-copied through JSON.
	// Replace it before cloning so callers still receive a valid, closed response.
	if !validJSON(cfg.EmbeddingSearchConfig.EmbeddingProvider.Parameters) {
		cfg.EmbeddingSearchConfig.EmbeddingProvider.Parameters = nil
	}

	out := *cfg.Clone()

	for i := range out.Services {
		redactServiceSecrets(&out.Services[i])
	}
	for i := range out.Bots {
		if out.Bots[i].Service != nil {
			redactServiceSecrets(out.Bots[i].Service)
		}
	}

	out.WebSearch.Google.APIKey = redactSecret(out.WebSearch.Google.APIKey)
	out.WebSearch.Brave.APIKey = redactSecret(out.WebSearch.Brave.APIKey)

	for i := range out.MCP.Servers {
		server := &out.MCP.Servers[i]
		server.ClientSecret = redactSecret(server.ClientSecret)
		redactHeaderSecrets(server.Headers)
		redactHeaderSecrets(server.ServiceAccountHeaders)
	}

	out.EmbeddingSearchConfig.EmbeddingProvider.Parameters = redactEmbeddingSecrets(
		out.EmbeddingSearchConfig.EmbeddingProvider.Parameters,
	)

	return out
}

// RestoreSecrets returns a copy of incoming with exact FakeSetting values
// replaced by their stored counterparts. Other values, including empty values,
// are preserved.
func RestoreSecrets(incoming, stored Config) Config {
	out := *incoming.Clone()

	for i := range out.Services {
		restoreServiceSecrets(&out.Services[i], findServiceByID(stored.Services, out.Services[i].ID))
	}
	for i := range out.Bots {
		if out.Bots[i].Service == nil {
			continue
		}

		var storedService *llm.ServiceConfig
		if storedBot := findBotByID(stored.Bots, out.Bots[i].ID); storedBot != nil {
			storedService = storedBot.Service
		}
		restoreServiceSecrets(out.Bots[i].Service, storedService)
	}

	out.WebSearch.Google.APIKey = restoreSecret(out.WebSearch.Google.APIKey, stored.WebSearch.Google.APIKey)
	out.WebSearch.Brave.APIKey = restoreSecret(out.WebSearch.Brave.APIKey, stored.WebSearch.Brave.APIKey)

	for i := range out.MCP.Servers {
		server := &out.MCP.Servers[i]
		storedServer := findMCPServer(stored.MCP.Servers, *server)
		if storedServer == nil {
			server.ClientSecret = restoreSecret(server.ClientSecret, "")
			restoreHeaderSecrets(server.Headers, nil)
			restoreHeaderSecrets(server.ServiceAccountHeaders, nil)
			continue
		}

		server.ClientSecret = restoreSecret(server.ClientSecret, storedServer.ClientSecret)
		restoreHeaderSecrets(server.Headers, storedServer.Headers)
		restoreHeaderSecrets(server.ServiceAccountHeaders, storedServer.ServiceAccountHeaders)
	}

	out.EmbeddingSearchConfig.EmbeddingProvider.Parameters = restoreEmbeddingSecrets(
		out.EmbeddingSearchConfig.EmbeddingProvider.Parameters,
		stored.EmbeddingSearchConfig.EmbeddingProvider.Parameters,
	)

	return out
}

func redactSecret(value string) string {
	if value == "" {
		return ""
	}
	return FakeSetting
}

func restoreSecret(incoming, stored string) string {
	if incoming != FakeSetting {
		return incoming
	}
	return stored
}

func redactServiceSecrets(service *llm.ServiceConfig) {
	service.APIKey = redactSecret(service.APIKey)
	service.AWSSecretAccessKey = redactSecret(service.AWSSecretAccessKey)
	service.VertexAuthCredentials = redactSecret(service.VertexAuthCredentials)
}

func restoreServiceSecrets(service *llm.ServiceConfig, stored *llm.ServiceConfig) {
	if stored == nil {
		stored = &llm.ServiceConfig{}
	}
	service.APIKey = restoreSecret(service.APIKey, stored.APIKey)
	service.AWSSecretAccessKey = restoreSecret(service.AWSSecretAccessKey, stored.AWSSecretAccessKey)
	service.VertexAuthCredentials = restoreSecret(service.VertexAuthCredentials, stored.VertexAuthCredentials)
}

func findServiceByID(services []llm.ServiceConfig, id string) *llm.ServiceConfig {
	if id == "" {
		return nil
	}
	for i := range services {
		if services[i].ID == id {
			return &services[i]
		}
	}
	return nil
}

func findBotByID(bots []llm.BotConfig, id string) *llm.BotConfig {
	if id == "" {
		return nil
	}
	for i := range bots {
		if bots[i].ID == id {
			return &bots[i]
		}
	}
	return nil
}

func findMCPServer(servers []MCPServerConfig, incoming MCPServerConfig) *MCPServerConfig {
	incomingEndpoint := CanonicalMCPEndpointURL(incoming.BaseURL)
	for i := range servers {
		if servers[i].Name == incoming.Name && CanonicalMCPEndpointURL(servers[i].BaseURL) == incomingEndpoint {
			return &servers[i]
		}
	}

	if incomingEndpoint == "" {
		return nil
	}

	var matched *MCPServerConfig
	for i := range servers {
		if CanonicalMCPEndpointURL(servers[i].BaseURL) == incomingEndpoint {
			if matched != nil {
				return nil
			}
			matched = &servers[i]
		}
	}
	return matched
}

func redactHeaderSecrets(headers map[string]string) {
	for key, value := range headers {
		headers[key] = redactSecret(value)
	}
}

func restoreHeaderSecrets(incoming, stored map[string]string) {
	for key, value := range incoming {
		incoming[key] = restoreSecret(value, stored[key])
	}
}

func validJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || json.Valid(raw)
}

func redactEmbeddingSecrets(raw json.RawMessage) json.RawMessage {
	params, ok := decodeParameters(raw)
	if !ok {
		return nil
	}

	for _, field := range embeddingCredentialFields {
		value, present := params[field]
		if !present || isExplicitEmptyString(value) {
			continue
		}
		params[field] = mustMarshalString(FakeSetting)
	}

	return encodeParameters(params)
}

func restoreEmbeddingSecrets(incoming, stored json.RawMessage) json.RawMessage {
	params, ok := decodeParameters(incoming)
	if !ok {
		return nil
	}
	storedParams, _ := decodeParameters(stored)

	changed := false
	for _, field := range embeddingCredentialFields {
		value, ok := parameterString(params, field)
		if !ok || value != FakeSetting {
			continue
		}

		storedValue, ok := storedParams[field]
		if !ok {
			storedValue = mustMarshalString("")
		}
		params[field] = storedValue
		changed = true
	}
	if !changed {
		return incoming
	}

	return encodeParameters(params)
}

func decodeParameters(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, true
	}

	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, false
	}
	return params, true
}

func parameterString(params map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := params[key]
	if !ok {
		return "", false
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func isExplicitEmptyString(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	stringValue, ok := value.(string)
	return ok && stringValue == ""
}

func mustMarshalString(value string) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func encodeParameters(params map[string]json.RawMessage) json.RawMessage {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return raw
}
