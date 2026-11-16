// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// SecretPlaceholder stands in for a configured credential in responses that
// leave the server. It mirrors the platform convention (model.FakeSetting).
const SecretPlaceholder = "********************************"

// embeddingProviderAPIKeyParam is the embedding provider parameter that holds a credential.
const embeddingProviderAPIKeyParam = "apiKey"

// IsSecretPlaceholder reports whether v is the mask itself. Any other value,
// including a shorter run of asterisks, is a real value the admin entered.
func IsSecretPlaceholder(v string) bool {
	return v == SecretPlaceholder
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
		redactHeaderMap(server.Headers)
		redactHeaderMap(server.ServiceAccountHeaders)
	}

	out.WebSearch.Google.APIKey = maskSecret(out.WebSearch.Google.APIKey)
	out.WebSearch.Brave.APIKey = maskSecret(out.WebSearch.Brave.APIKey)
	out.OpenTelemetryHeaders = maskSecret(out.OpenTelemetryHeaders)

	provider := &out.EmbeddingSearchConfig.EmbeddingProvider
	provider.Parameters = redactEmbeddingProviderSecrets(provider.Parameters)

	return out
}

// RestoreSecrets returns a copy of incoming with every masked credential
// replaced by its stored counterpart. Other values pass through, so an empty
// value clears the credential. A mask with no counterpart is left in place; check
// the result with MaskedCredentialFields before saving it.
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

	matchedServers := matchStoredMCPServers(out.MCP.Servers, stored.MCP.Servers)
	for i := range out.MCP.Servers {
		server := &out.MCP.Servers[i]

		var storedHeaders map[string]string
		var storedServiceAccountHeaders map[string]string
		storedClientSecret := ""
		if storedServer := matchedServers[i]; storedServer != nil {
			storedHeaders = storedServer.Headers
			storedServiceAccountHeaders = storedServer.ServiceAccountHeaders
			storedClientSecret = storedServer.ClientSecret
		}

		server.ClientSecret = resolveSecret(server.ClientSecret, storedClientSecret)
		server.Headers = restoreHeaderSecrets(server.Headers, storedHeaders)
		server.ServiceAccountHeaders = restoreHeaderSecrets(server.ServiceAccountHeaders, storedServiceAccountHeaders)
	}

	out.WebSearch.Google.APIKey = resolveSecret(out.WebSearch.Google.APIKey, stored.WebSearch.Google.APIKey)
	out.WebSearch.Brave.APIKey = resolveSecret(out.WebSearch.Brave.APIKey, stored.WebSearch.Brave.APIKey)
	out.OpenTelemetryHeaders = resolveSecret(out.OpenTelemetryHeaders, stored.OpenTelemetryHeaders)

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

func redactHeaderMap(headers map[string]string) {
	for name, value := range headers {
		headers[name] = maskSecret(value)
	}
}

// resolveSecret returns incoming unless it is the mask, which is replaced by
// stored. A mask with no stored secret to resolve it stays a mask, so that
// MaskedCredentialFields can refuse the save instead of persisting it.
func resolveSecret(incoming, stored string) string {
	if !IsSecretPlaceholder(incoming) || stored == "" || IsSecretPlaceholder(stored) {
		return incoming
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

// mcpServerIdentifiers recognize an incoming MCP server as a stored one, most
// specific first. A server keeps its credentials when its URL or name is edited:
// the ID names it, and servers saved before IDs existed fall back to name and URL.
var mcpServerIdentifiers = []func(incoming, stored MCPServerConfig) bool{
	func(incoming, stored MCPServerConfig) bool {
		return incoming.ID != "" && incoming.ID == stored.ID
	},
	func(incoming, stored MCPServerConfig) bool {
		return incoming.Name == stored.Name && incoming.BaseURL == stored.BaseURL
	},
	func(incoming, stored MCPServerConfig) bool {
		return incoming.BaseURL == stored.BaseURL
	},
	func(incoming, stored MCPServerConfig) bool {
		return incoming.Name == stored.Name
	},
}

// matchStoredMCPServers pairs each incoming MCP server with the stored entry it
// identifies, or nil. Each stored entry is claimed at most once.
func matchStoredMCPServers(incoming, stored []MCPServerConfig) []*MCPServerConfig {
	matched := make([]*MCPServerConfig, len(incoming))
	claimed := make([]bool, len(stored))

	for _, identifies := range mcpServerIdentifiers {
		for i := range incoming {
			if matched[i] != nil {
				continue
			}

			found := -1
			for j := range stored {
				if claimed[j] || !identifies(incoming[i], stored[j]) {
					continue
				}
				if found >= 0 {
					found = -1
					break
				}
				found = j
			}

			if found >= 0 {
				matched[i] = &stored[found]
				claimed[found] = true
			}
		}
	}

	return matched
}

// restoreHeaderSecrets resolves masked header values against the stored headers
// of the same server. A mask under a new key takes the one stored header left
// unclaimed (a renamed header); anything less clear-cut stays a mask.
func restoreHeaderSecrets(incoming, stored map[string]string) map[string]string {
	if len(incoming) == 0 {
		return incoming
	}

	unclaimed := make(map[string]string, len(stored))
	maps.Copy(unclaimed, stored)

	var unmatched []string
	for key, value := range incoming {
		if storedValue, ok := unclaimed[key]; ok {
			delete(unclaimed, key)
			incoming[key] = resolveSecret(value, storedValue)
			continue
		}
		if IsSecretPlaceholder(value) {
			unmatched = append(unmatched, key)
		}
	}

	if len(unmatched) == 1 && len(unclaimed) == 1 {
		for _, storedValue := range unclaimed {
			incoming[unmatched[0]] = storedValue
		}
		return incoming
	}

	return incoming
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

// ErrUnresolvedSecret is returned when a configuration to be saved still holds
// the mask in a credential field.
var ErrUnresolvedSecret = errors.New("masked credential has no stored value")

// MaskedCredentialFields lists the credential fields of cfg that still hold the
// exact mask, by path. It never includes a credential value. A saved
// configuration must have none: the mask is not a credential.
func MaskedCredentialFields(cfg Config) []string {
	var fields []string
	add := func(path, value string) {
		if IsSecretPlaceholder(value) {
			fields = append(fields, path)
		}
	}
	service := func(prefix string, svc *llm.ServiceConfig) {
		add(prefix+".apiKey", svc.APIKey)
		add(prefix+".awsSecretAccessKey", svc.AWSSecretAccessKey)
		add(prefix+".vertexAuthCredentials", svc.VertexAuthCredentials)
	}

	for _, svc := range cfg.Services {
		service("services["+svc.ID+"]", &svc)
	}
	for _, bot := range cfg.Bots {
		if bot.Service != nil {
			service("bots["+bot.ID+"].service", bot.Service)
		}
	}
	for _, server := range cfg.MCP.Servers {
		prefix := "mcp.servers[" + server.Name + "]"
		add(prefix+".clientSecret", server.ClientSecret)
		for name, value := range server.Headers {
			add(prefix+".headers["+name+"]", value)
		}
		for name, value := range server.ServiceAccountHeaders {
			add(prefix+".serviceAccountHeaders["+name+"]", value)
		}
	}
	add("webSearch.google.apiKey", cfg.WebSearch.Google.APIKey)
	add("webSearch.brave.apiKey", cfg.WebSearch.Brave.APIKey)
	add("openTelemetryHeaders", cfg.OpenTelemetryHeaders)
	if params, ok := decodeParameters(cfg.EmbeddingSearchConfig.EmbeddingProvider.Parameters); ok {
		if value, isString := parameterString(params, embeddingProviderAPIKeyParam); isString {
			add("embeddingSearchConfig.embeddingProvider.parameters."+embeddingProviderAPIKeyParam, value)
		}
	}

	slices.Sort(fields)
	return fields
}
