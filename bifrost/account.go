// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// providerAccount implements the Bifrost Account interface for a single provider.
type providerAccount struct {
	ProviderSettings

	// name is the Bifrost registration name: empty for a standard provider, or
	// a unique custom-provider name when this account shares a base provider
	// type with another in the same client (which would otherwise collide on
	// the base type's single slot).
	name     schemas.ModelProvider
	keyless  bool
	chatOnly bool
}

// registeredName returns the name this account is registered under with Bifrost,
// defaulting to the base provider type when no custom name is set.
func (a *providerAccount) registeredName() schemas.ModelProvider {
	if a.name != "" {
		return a.name
	}
	return a.Provider
}

// isCustom reports whether this account is registered as a Bifrost custom
// provider (a unique name backed by the base provider type), which lets two
// services sharing a base provider type keep their own base URL and credentials.
func (a *providerAccount) isCustom() bool {
	return a.name != "" && a.name != a.Provider
}

func (a *providerAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{a.registeredName()}, nil
}

func (a *providerAccount) GetKeysForProvider(ctx context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	if provider != a.registeredName() {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}

	key := schemas.Key{
		Value:  schemas.SecretVar{Val: a.APIKey},
		Weight: 1.0,
		// Bifrost v1.5+ requires keys to declare which models they support;
		// "*" allows any model the configured provider can serve.
		Models: schemas.WhiteList{"*"},
	}

	// Handle Azure config
	if a.Provider == schemas.Azure && a.APIURL != "" {
		key.AzureKeyConfig = &schemas.AzureKeyConfig{
			Endpoint: schemas.SecretVar{Val: a.APIURL},
		}
	}

	// Handle Bedrock config
	if a.Provider == schemas.Bedrock {
		region := schemas.SecretVar{Val: a.Region}
		key.BedrockKeyConfig = &schemas.BedrockKeyConfig{
			AccessKey: schemas.SecretVar{Val: a.AWSAccessKeyID},
			SecretKey: schemas.SecretVar{Val: a.AWSSecretAccessKey},
			Region:    &region,
		}
	}

	// Handle Vertex config. Empty AuthCredentials signals ADC / attached IAM role.
	if a.Provider == schemas.Vertex {
		key.VertexKeyConfig = &schemas.VertexKeyConfig{
			ProjectID:       schemas.SecretVar{Val: a.VertexProjectID},
			ProjectNumber:   schemas.SecretVar{Val: a.VertexProjectNumber},
			Region:          schemas.SecretVar{Val: a.Region},
			AuthCredentials: schemas.SecretVar{Val: a.VertexAuthCredentials},
		}
	}

	return []schemas.Key{key}, nil
}

func (a *providerAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	if provider != a.registeredName() {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}

	networkConfig := schemas.DefaultNetworkConfig

	// Pass through the streaming timeout to the Bifrost HTTP client so that
	// long-running requests (e.g. thinking models) are not killed by the
	// underlying fasthttp ReadTimeout before the watchdog timer fires.
	if a.StreamingTimeout > 0 {
		networkConfig.DefaultRequestTimeoutInSeconds = int(a.StreamingTimeout.Seconds()) * 10
	} else {
		networkConfig.DefaultRequestTimeoutInSeconds = int(DefaultStreamingTimeout.Seconds()) * 10
	}

	// Use BaseURL for providers that support custom endpoints (not Azure, which uses AzureKeyConfig)
	if a.APIURL != "" && a.Provider != schemas.Azure {
		networkConfig.BaseURL = a.APIURL
	}

	// Pass OrgID via ExtraHeaders for OpenAI
	if a.OrgID != "" && a.Provider == schemas.OpenAI {
		networkConfig.ExtraHeaders = map[string]string{
			"OpenAI-Organization": a.OrgID,
		}
	}

	// Configure retry logic with sensible defaults
	networkConfig.MaxRetries = 2
	networkConfig.RetryBackoffInitial = 1 * time.Second
	networkConfig.RetryBackoffMax = 10 * time.Second

	config := &schemas.ProviderConfig{
		NetworkConfig:            networkConfig,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
		ProxyConfig: &schemas.ProxyConfig{
			Type: schemas.EnvProxy,
		},
	}

	if a.isCustom() {
		cpc := &schemas.CustomProviderConfig{
			BaseProviderType: a.Provider,
			IsKeyLess:        a.keyless,
		}
		if a.chatOnly {
			// Chat-only AllowedRequests makes Bifrost transparently downgrade a
			// Responses-API request to /v1/chat/completions instead of POSTing
			// /v1/responses to an endpoint that does not implement it.
			cpc.AllowedRequests = &schemas.AllowedRequests{
				ChatCompletion:       true,
				ChatCompletionStream: true,
			}
		}
		config.CustomProviderConfig = cpc
	}

	return config, nil
}

// multiProviderAccount implements the Bifrost Account interface for the
// primary provider plus every fallback in the chain.
type multiProviderAccount struct {
	entries map[schemas.ModelProvider]*providerAccount
	order   []schemas.ModelProvider
}

func newMultiProviderAccount() *multiProviderAccount {
	return &multiProviderAccount{
		entries: make(map[schemas.ModelProvider]*providerAccount),
	}
}

// isCustomCapableProvider reports whether Bifrost can host a second instance of
// this provider type as a custom provider backed by the same base type. Only the
// providers in schemas.SupportedBaseProviders qualify; others (e.g. Azure,
// Mistral, Vertex) cannot be disambiguated this way.
func isCustomCapableProvider(p schemas.ModelProvider) bool {
	return slices.Contains(schemas.SupportedBaseProviders, p)
}

// customProviderName builds a stable, unique Bifrost custom-provider name for a
// service that shares a base provider type with another in the same client.
func customProviderName(base schemas.ModelProvider, serviceID string) schemas.ModelProvider {
	return schemas.ModelProvider(fmt.Sprintf("%s::%s", base, serviceID))
}

// addProvider registers a provider under its Bifrost registration name.
// First-registered wins on a name collision, because Bifrost indexes by name.
func (m *multiProviderAccount) addProvider(entry *providerAccount) {
	name := entry.registeredName()
	if _, exists := m.entries[name]; exists {
		return
	}
	m.entries[name] = entry
	m.order = append(m.order, name)
}

func (m *multiProviderAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return m.order, nil
}

func (m *multiProviderAccount) GetKeysForProvider(ctx context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	entry, ok := m.entries[provider]
	if !ok {
		return nil, fmt.Errorf("provider %s not configured", provider)
	}
	return entry.GetKeysForProvider(ctx, provider)
}

func (m *multiProviderAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	entry, ok := m.entries[provider]
	if !ok {
		return nil, fmt.Errorf("provider %s not configured", provider)
	}
	return entry.GetConfigForProvider(provider)
}
