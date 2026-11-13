// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// SecretPlaceholder stands in for a configured credential in responses that
// leave the server. It mirrors the platform convention (model.FakeSetting).
const SecretPlaceholder = "********************************"

// embeddingProviderAPIKeyParam is the embedding provider parameter that holds a credential.
const embeddingProviderAPIKeyParam = "apiKey"

// embeddingProviderAPIURLParam and embeddingProviderProviderParam are the embedding
// provider parameters holding the endpoint and the upstream the key is sent to.
const (
	embeddingProviderAPIURLParam   = "apiURL"
	embeddingProviderProviderParam = "provider"
)

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
// value clears the credential; a mask with no counterpart resolves to empty.
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

	out.WebSearch.Google.APIKey = resolveBoundSecret(
		out.WebSearch.Google.APIKey, destination(out.WebSearch.Google.APIURL),
		stored.WebSearch.Google.APIKey, destination(stored.WebSearch.Google.APIURL))
	out.WebSearch.Brave.APIKey = resolveBoundSecret(
		out.WebSearch.Brave.APIKey, destination(out.WebSearch.Brave.APIURL),
		stored.WebSearch.Brave.APIKey, destination(stored.WebSearch.Brave.APIURL))
	out.OpenTelemetryHeaders = resolveBoundSecret(
		out.OpenTelemetryHeaders, destination(out.OpenTelemetryEndpoint, strconv.FormatBool(out.OpenTelemetryTLS)),
		stored.OpenTelemetryHeaders, destination(stored.OpenTelemetryEndpoint, strconv.FormatBool(stored.OpenTelemetryTLS)))

	provider := &out.EmbeddingSearchConfig.EmbeddingProvider
	provider.Parameters = restoreEmbeddingProviderSecrets(*provider, stored.EmbeddingSearchConfig.EmbeddingProvider)

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

// destination identifies where a credential is sent: the parts (provider type,
// URL) are compared as written, except that URLs ignore surrounding spaces and
// trailing slashes. An empty URL is the provider's own fixed address.
func destination(parts ...string) string {
	normalized := make([]string, len(parts))
	for i, part := range parts {
		normalized[i] = strings.TrimRight(strings.TrimSpace(part), "/")
	}
	return strings.Join(normalized, "\x00")
}

// resolveBoundSecret is resolveSecret for a credential that belongs to the
// destination it was saved with: a mask only resolves when the destination is
// unchanged, otherwise it resolves to empty so the stored credential is never
// sent somewhere the admin pointed it at later.
func resolveBoundSecret(incoming, incomingDestination, stored, storedDestination string) string {
	if IsSecretPlaceholder(incoming) && incomingDestination != storedDestination {
		return ""
	}
	return resolveSecret(incoming, stored)
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

// BindsCredentials reports whether a request for the given service type and API
// URL may use the credentials stored on service. They belong to the provider
// and endpoint they were saved with; an empty URL is the provider's own address.
func BindsCredentials(service llm.ServiceConfig, serviceType, apiURL string) bool {
	return destination(service.Type, service.APIURL) == destination(serviceType, apiURL)
}

func restoreServiceSecrets(service *llm.ServiceConfig, stored *llm.ServiceConfig) {
	if stored == nil || !BindsCredentials(*stored, service.Type, service.APIURL) {
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

// mcpServerIdentifiers recognize an incoming MCP server as a stored one, most specific first.
var mcpServerIdentifiers = []func(incoming, stored MCPServerConfig) bool{
	func(incoming, stored MCPServerConfig) bool {
		return incoming.Name == stored.Name && incoming.BaseURL == stored.BaseURL
	},
	func(incoming, stored MCPServerConfig) bool {
		return incoming.BaseURL == stored.BaseURL
	},
}

// matchStoredMCPServers pairs each incoming MCP server with the stored entry it
// identifies, or nil. Credentials stay with the base URL they were stored
// against, and each stored entry is claimed at most once.
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
// unclaimed (a renamed header); anything less clear-cut resolves to empty.
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

	for _, key := range unmatched {
		incoming[key] = ""
	}

	return incoming
}

// ValidateEmbeddingProviderParameters rejects embedding provider parameters the
// credential binding cannot reason about: the key, URL or provider repeated
// under another capitalization (which spelling the client reads would depend on
// key order), or values the provider's client could not decode.
func ValidateEmbeddingProviderParameters(upstream embeddings.UpstreamConfig) error {
	if len(upstream.Parameters) == 0 {
		return nil
	}
	params, ok := decodeParameters(upstream.Parameters)
	if !ok {
		return errors.New("embedding provider parameters must be a JSON object")
	}
	for _, name := range []string{embeddingProviderAPIKeyParam, embeddingProviderAPIURLParam, embeddingProviderProviderParam} {
		if len(parameterKeys(params, name)) > 1 {
			return fmt.Errorf("embedding provider parameter %q is set more than once", name)
		}
	}
	switch upstream.Type {
	case embeddings.ProviderTypeBifrost, embeddings.ProviderTypeOpenAI, embeddings.ProviderTypeOpenAICompatible:
		if _, _, err := upstream.Credential(); err != nil {
			return err
		}
	}
	return nil
}

// parameterKeys returns every key of params that names the parameter, however capitalized.
func parameterKeys(params map[string]json.RawMessage, name string) []string {
	var keys []string
	for key := range params {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	return keys
}

func redactEmbeddingProviderSecrets(parameters json.RawMessage) json.RawMessage {
	params, ok := decodeParameters(parameters)
	if !ok {
		return parameters
	}

	redacted := false
	for _, key := range parameterKeys(params, embeddingProviderAPIKeyParam) {
		if value, isString := parameterString(params, key); isString && value != "" {
			params[key] = jsonString(SecretPlaceholder)
			redacted = true
		}
	}
	if !redacted {
		return parameters
	}

	return encodeParameters(params, parameters)
}

func restoreEmbeddingProviderSecrets(provider, stored embeddings.UpstreamConfig) json.RawMessage {
	params, ok := decodeParameters(provider.Parameters)
	if !ok {
		return provider.Parameters
	}

	// Only a provider whose parameters decode the way its client decodes them,
	// on both sides, has a destination to bind to; otherwise nothing is restored.
	_, incomingDestination, incomingErr := provider.Credential()
	storedKey, storedDestination, storedErr := stored.Credential()
	bindable := incomingErr == nil && storedErr == nil

	restored := false
	for _, key := range parameterKeys(params, embeddingProviderAPIKeyParam) {
		value, isString := parameterString(params, key)
		if !isString || !IsSecretPlaceholder(value) {
			continue
		}
		resolved := ""
		if bindable {
			resolved = resolveBoundSecret(value, destination(incomingDestination...), storedKey, destination(storedDestination...))
		}
		params[key] = jsonString(resolved)
		restored = true
	}
	if !restored {
		return provider.Parameters
	}

	return encodeParameters(params, provider.Parameters)
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

// jsonString encodes a string as a JSON value; marshaling a string cannot fail.
func jsonString(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

// encodeParameters returns params as JSON, falling back to the original when it cannot be encoded.
func encodeParameters(params map[string]json.RawMessage, fallback json.RawMessage) json.RawMessage {
	encoded, err := json.Marshal(params)
	if err != nil {
		return fallback
	}

	return encoded
}
