// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	tokenUsageLogToPluginEnvKey = "MM_FEATUREFLAGS_AI_TOKEN_USAGE_LOG_TO_PLUGIN" // #nosec G101 -- env var key name, not a credential
	tokenUsageLogToFileEnvKey   = "MM_FEATUREFLAGS_AI_TOKEN_USAGE_LOG_TO_FILE"   // #nosec G101 -- env var key name, not a credential
)

type Config struct {
	Services                        []llm.ServiceConfig              `json:"services"`
	Bots                            []llm.BotConfig                  `json:"bots"`
	DefaultBotName                  string                           `json:"defaultBotName"`
	TranscriptGenerator             string                           `json:"transcriptBackend"`
	EnableTokenUsageLogging         bool                             `json:"enableTokenUsageLogging"`
	EnableCallSummary               bool                             `json:"enableCallSummary"`
	EnableTokenUsageLogToPlugin     *bool                            `json:"enableTokenUsageLogToPlugin,omitempty"`
	EnableTokenUsageLogToFile       *bool                            `json:"enableTokenUsageLogToFile,omitempty"`
	AllowedUpstreamHostnames        string                           `json:"allowedUpstreamHostnames"`
	AllowUnsafeLinks                bool                             `json:"allowUnsafeLinks"`
	EnableChannelMentionToolCalling bool                             `json:"enableChannelMentionToolCalling"`
	AllowNativeWebSearchInChannels  bool                             `json:"allowNativeWebSearchInChannels"`
	EmbeddingSearchConfig           embeddings.EmbeddingSearchConfig `json:"embeddingSearchConfig"`
	MCP                             MCPConfig                        `json:"mcp"`
	WebSearch                       WebSearchConfig                  `json:"webSearch"`
	TelemetryOutput                 string                           `json:"telemetryOutput"`
	OpenTelemetryEndpoint           string                           `json:"openTelemetryEndpoint"`
}

type WebSearchConfig struct {
	Enabled        bool                  `json:"enabled"`
	Provider       string                `json:"provider"`
	Google         WebSearchGoogleConfig `json:"google"`
	Brave          WebSearchBraveConfig  `json:"brave"`
	DomainDenylist []string              `json:"domainDenylist"`
}

type WebSearchGoogleConfig struct {
	APIKey         string `json:"apiKey"`
	SearchEngineID string `json:"searchEngineId"`
	ResultLimit    int    `json:"resultLimit"`
	APIURL         string `json:"apiURL"`
}

type WebSearchBraveConfig struct {
	APIKey       string `json:"apiKey"`
	APIURL       string `json:"apiURL"`
	ResultLimit  int    `json:"resultLimit"`
	PollTimeout  int    `json:"pollTimeout"`
	PollInterval int    `json:"pollInterval"`
}

func (c *Config) Clone() *Config {
	clone, err := DeepCopyJSON(*c)
	if err != nil {
		panic(fmt.Sprintf("failed to clone configuration: %v", err))
	}

	return &clone
}

// GetServiceByID returns the service configuration for the given ID
func (c *Config) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	for i := range c.Services {
		if c.Services[i].ID == id {
			return c.Services[i], true
		}
	}
	return llm.ServiceConfig{}, false
}

type UpdateListener func()

type Container struct {
	cfg       atomic.Pointer[Config]
	listeners []UpdateListener
}

// Config returns the whole configuration readonly. It never returns nil: before
// the first Update it returns a zero-value configuration.
// Avoid using this method, prefer using config though interfaces.
func (c *Container) Config() *Config {
	if cfg := c.cfg.Load(); cfg != nil {
		return cfg
	}
	return &Config{}
}

func (c *Container) GetTranscriptGenerator() string {
	return c.Config().TranscriptGenerator
}

func (c *Container) GetBots() []llm.BotConfig {
	return c.Config().Bots
}

func (c *Container) GetDefaultBotName() string {
	return c.Config().DefaultBotName
}

func (c *Container) EnableTokenUsageLogging() bool {
	return c.Config().EnableTokenUsageLogging
}

func (c *Container) EnableTokenUsageLogToPlugin() bool {
	if !c.Config().EnableTokenUsageLogging {
		return false
	}

	if enabled, ok := parseBooleanEnv(tokenUsageLogToPluginEnvKey); ok {
		return enabled
	}

	return false
}

func (c *Container) EnableTokenUsageLogToFile() bool {
	if !c.Config().EnableTokenUsageLogging {
		return false
	}

	if enabled, ok := parseBooleanEnv(tokenUsageLogToFileEnvKey); ok {
		return enabled
	}

	return true
}

func parseBooleanEnv(key string) (bool, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return false, false
	}

	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}

	return parsed, true
}

func (c *Container) MCP() MCPConfig {
	return c.Config().MCP
}

func (c *Container) AllowUnsafeLinks() bool {
	return c.Config().AllowUnsafeLinks
}

func (c *Container) EnableChannelMentionToolCalling() bool {
	return c.Config().EnableChannelMentionToolCalling
}

func (c *Container) AllowNativeWebSearchInChannels() bool {
	return c.Config().AllowNativeWebSearchInChannels
}

func (c *Container) RegisterUpdateListener(listener UpdateListener) {
	c.listeners = append(c.listeners, listener)
}

func (c *Container) EmbeddingSearchConfig() embeddings.EmbeddingSearchConfig {
	return c.Config().EmbeddingSearchConfig
}

// GetServices returns a shallow copy of the configured services so callers can
// hold and iterate a stable snapshot while the container is updated.
func (c *Container) GetServices() []llm.ServiceConfig {
	cfg := c.cfg.Load()
	if cfg == nil {
		return nil
	}
	return slices.Clone(cfg.Services)
}

// GetServiceByID returns the service configuration for the given ID
func (c *Container) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	return c.Config().GetServiceByID(id)
}

// Update replaces the current configuration and notifies all listeners.
// The new configuration is deep-copied to ensure the new and old
// configurations are independent of each other.
func (c *Container) Update(newConfig *Config) {
	clone, err := cloneConfig(newConfig)
	if err != nil {
		panic(err)
	}
	c.cfg.Store(clone)

	for _, listener := range c.listeners {
		listener()
	}
}

// StorePersistedConfigWithoutNotify updates in-memory configuration from a value read back from
// persistent storage without notifying update listeners. Use when the current call stack may
// already be servicing a listener (for example after SaveConfig during legacy migration) to
// avoid re-entrant listener invocation and deadlocks.
func (c *Container) StorePersistedConfigWithoutNotify(newConfig *Config) error {
	clone, err := cloneConfig(newConfig)
	if err != nil {
		return err
	}
	c.cfg.Store(clone)
	return nil
}

// cloneConfig deep-copies cfg so the stored configuration is independent of
// the caller's value. A nil cfg becomes a zero-value configuration, so the
// container never holds a nil pointer.
func cloneConfig(cfg *Config) (*Config, error) {
	if cfg == nil {
		return &Config{}, nil
	}
	clone, err := DeepCopyJSON(*cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to deep copy configuration: %w", err)
	}
	return &clone, nil
}

// DeepCopyJSON creates a deep copy of JSON-serializable structs
func DeepCopyJSON[T any](src T) (T, error) {
	var dst T
	data, err := json.Marshal(src)
	if err != nil {
		return dst, err
	}
	err = json.Unmarshal(data, &dst)
	return dst, err
}
