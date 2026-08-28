// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// SecretPlaceholder stands in for a configured credential in responses that
// leave the server. It mirrors the platform convention (model.FakeSetting).
const SecretPlaceholder = "********************************"

// embeddingProviderAPIKeyParam is the embedding provider parameter that holds a credential.
const embeddingProviderAPIKeyParam = "apiKey"

// IsSecretPlaceholder reports whether v is a mask rather than a credential.
func IsSecretPlaceholder(v string) bool {
	return v != "" && strings.Count(v, "*") == len(v)
}

// RedactSecrets returns a copy of cfg in which every configured credential is
// replaced by SecretPlaceholder. An unconfigured credential stays empty so
// callers can still tell the two apart.
func RedactSecrets(cfg Config) Config {
	out := *cfg.Clone()

	for i := range out.Services {
		redactServiceSecrets(&out.Services[i])
	}

	for i := range out.Bots {
		if out.Bots[i].Service != nil {
			redactServiceSecrets(out.Bots[i].Service)
		}
	}

	for i := range out.MCP.Servers {
		server := &out.MCP.Servers[i]
		server.ClientSecret = maskSecret(server.ClientSecret)
		for name, value := range server.Headers {
			server.Headers[name] = maskSecret(value)
		}
	}

	out.WebSearch.Google.APIKey = maskSecret(out.WebSearch.Google.APIKey)
	out.WebSearch.Brave.APIKey = maskSecret(out.WebSearch.Brave.APIKey)

	provider := &out.EmbeddingSearchConfig.EmbeddingProvider
	provider.Parameters = redactEmbeddingProviderSecrets(provider.Parameters)

	return out
}

// RestoreSecrets returns a copy of incoming in which every masked credential is
// replaced by the value held in stored. Any other value passes through, so an
// empty value clears the credential. A mask without a counterpart in stored
// resolves to empty instead of being persisted verbatim.
//
// Counterparts are matched by service ID, bot ID (for the deprecated inline
// service), MCP server name, header key within that server, and parameter key
// for the embedding provider.
func RestoreSecrets(incoming Config, stored *Config) Config {
	out := *incoming.Clone()
	if stored == nil {
		stored = &Config{}
	}

	for i := range out.Services {
		restoreServiceSecrets(&out.Services[i], findServiceByID(stored.Services, out.Services[i].ID))
	}

	for i := range out.Bots {
		if out.Bots[i].Service == nil {
			continue
		}
		var storedInline *llm.ServiceConfig
		if storedBot := findBotByID(stored.Bots, out.Bots[i].ID); storedBot != nil {
			storedInline = storedBot.Service
		}
		restoreServiceSecrets(out.Bots[i].Service, storedInline)
	}

	for i := range out.MCP.Servers {
		server := &out.MCP.Servers[i]
		storedServer := findMCPServerByName(stored.MCP.Servers, server.Name)
		storedHeaders := map[string]string{}
		storedClientSecret := ""
		if storedServer != nil {
			storedHeaders = storedServer.Headers
			storedClientSecret = storedServer.ClientSecret
		}

		server.ClientSecret = resolveSecret(server.ClientSecret, storedClientSecret)
		for name, value := range server.Headers {
			server.Headers[name] = resolveSecret(value, storedHeaders[name])
		}
	}

	out.WebSearch.Google.APIKey = resolveSecret(out.WebSearch.Google.APIKey, stored.WebSearch.Google.APIKey)
	out.WebSearch.Brave.APIKey = resolveSecret(out.WebSearch.Brave.APIKey, stored.WebSearch.Brave.APIKey)

	provider := &out.EmbeddingSearchConfig.EmbeddingProvider
	provider.Parameters = restoreEmbeddingProviderSecrets(
		provider.Parameters,
		stored.EmbeddingSearchConfig.EmbeddingProvider.Parameters,
	)

	return out
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	return SecretPlaceholder
}

func resolveSecret(incoming, stored string) string {
	if !IsSecretPlaceholder(incoming) {
		return incoming
	}
	if IsSecretPlaceholder(stored) {
		return ""
	}
	return stored
}

func redactServiceSecrets(service *llm.ServiceConfig) {
	service.APIKey = maskSecret(service.APIKey)
	service.AWSSecretAccessKey = maskSecret(service.AWSSecretAccessKey)
	service.VertexAuthCredentials = maskSecret(service.VertexAuthCredentials)
}

func restoreServiceSecrets(service *llm.ServiceConfig, stored *llm.ServiceConfig) {
	if stored == nil {
		stored = &llm.ServiceConfig{}
	}
	service.APIKey = resolveSecret(service.APIKey, stored.APIKey)
	service.AWSSecretAccessKey = resolveSecret(service.AWSSecretAccessKey, stored.AWSSecretAccessKey)
	service.VertexAuthCredentials = resolveSecret(service.VertexAuthCredentials, stored.VertexAuthCredentials)
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

func findMCPServerByName(servers []MCPServerConfig, name string) *MCPServerConfig {
	if name == "" {
		return nil
	}
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i]
		}
	}
	return nil
}

func redactEmbeddingProviderSecrets(parameters json.RawMessage) json.RawMessage {
	params, ok := decodeParameters(parameters)
	if !ok {
		return parameters
	}

	value, ok := parameterString(params, embeddingProviderAPIKeyParam)
	if !ok || value == "" {
		return parameters
	}

	return encodeParameters(params, embeddingProviderAPIKeyParam, SecretPlaceholder, parameters)
}

func restoreEmbeddingProviderSecrets(parameters, stored json.RawMessage) json.RawMessage {
	params, ok := decodeParameters(parameters)
	if !ok {
		return parameters
	}

	value, ok := parameterString(params, embeddingProviderAPIKeyParam)
	if !ok || !IsSecretPlaceholder(value) {
		return parameters
	}

	storedParams, _ := decodeParameters(stored)
	storedValue, _ := parameterString(storedParams, embeddingProviderAPIKeyParam)

	return encodeParameters(params, embeddingProviderAPIKeyParam, resolveSecret(value, storedValue), parameters)
}

// decodeParameters keeps unrelated entries as raw JSON so re-encoding preserves them verbatim.
func decodeParameters(parameters json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(parameters) == 0 {
		return nil, false
	}

	params := map[string]json.RawMessage{}
	if err := json.Unmarshal(parameters, &params); err != nil {
		return nil, false
	}

	return params, true
}

func parameterString(params map[string]json.RawMessage, key string) (string, bool) {
	encoded, ok := params[key]
	if !ok {
		return "", false
	}

	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		return "", false
	}

	return value, true
}

// encodeParameters returns params with key set to value, falling back to the
// original JSON when it cannot be re-encoded.
func encodeParameters(params map[string]json.RawMessage, key, value string, fallback json.RawMessage) json.RawMessage {
	encodedValue, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	params[key] = encodedValue

	encoded, err := json.Marshal(params)
	if err != nil {
		return fallback
	}

	return encoded
}
