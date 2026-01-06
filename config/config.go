// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/openai"
)

type Config struct {
	Services                 []llm.ServiceConfig              `json:"services"`
	Bots                     []llm.BotConfig                  `json:"bots"`
	DefaultBotName           string                           `json:"defaultBotName"`
	TranscriptGenerator      string                           `json:"transcriptBackend"`
	EnableLLMTrace           bool                             `json:"enableLLMTrace"`
	EnableTokenUsageLogging  bool                             `json:"enableTokenUsageLogging"`
	AllowedUpstreamHostnames string                           `json:"allowedUpstreamHostnames"`
	AllowUnsafeLinks         bool                             `json:"allowUnsafeLinks"`
	EmbeddingSearchConfig    embeddings.EmbeddingSearchConfig `json:"embeddingSearchConfig"`
	MCP                      mcp.Config                       `json:"mcp"`
	WebSearch                WebSearchConfig                  `json:"webSearch"`
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

// Config retruns the whole configuration readonly.
// Avoid using this method, prefer using config though interfaces.
func (c *Container) Config() *Config {
	return c.cfg.Load()
}

func (c *Container) GetEnableLLMTrace() bool {
	return c.cfg.Load().EnableLLMTrace
}

func (c *Container) GetTranscriptGenerator() string {
	return c.cfg.Load().TranscriptGenerator
}

func (c *Container) GetBots() []llm.BotConfig {
	return c.cfg.Load().Bots
}

func (c *Container) GetDefaultBotName() string {
	return c.cfg.Load().DefaultBotName
}

func (c *Container) EnableLLMLogging() bool {
	return c.cfg.Load().EnableLLMTrace
}

func (c *Container) EnableTokenUsageLogging() bool {
	return c.cfg.Load().EnableTokenUsageLogging
}

func (c *Container) MCP() mcp.Config {
	return c.cfg.Load().MCP
}

func (c *Container) AllowUnsafeLinks() bool {
	cfg := c.cfg.Load()
	if cfg == nil {
		return false
	}

	return cfg.AllowUnsafeLinks
}

func (c *Container) RegisterUpdateListener(listener UpdateListener) {
	c.listeners = append(c.listeners, listener)
}

func (c *Container) EmbeddingSearchConfig() embeddings.EmbeddingSearchConfig {
	return c.cfg.Load().EmbeddingSearchConfig
}

// GetServiceByID returns the service configuration for the given ID
func (c *Container) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	cfg := c.cfg.Load()
	if cfg == nil {
		return llm.ServiceConfig{}, false
	}
	return cfg.GetServiceByID(id)
}

// Updates the current configuration
// The new configuration is deep-copied to ensure the new and old
// configurations are independent of each other.
func (c *Container) Update(newConfig *Config) {
	if newConfig == nil {
		c.cfg.Store(nil)
		return
	}
	// Create a deep copy of the new configuration
	clone, err := DeepCopyJSON(*newConfig)
	if err != nil {
		panic(fmt.Sprintf("failed to deep copy configuration: %v", err))
	}

	// Update the atomic pointer with the new configuration
	c.cfg.Store(&clone)

	// Notify all listeners about the configuration change
	for _, listener := range c.listeners {
		listener()
	}
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

func OpenAIConfigFromServiceConfig(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig) openai.Config {
	streamingTimeout := time.Second * 30
	if serviceConfig.StreamingTimeoutSeconds > 0 {
		streamingTimeout = time.Duration(serviceConfig.StreamingTimeoutSeconds) * time.Second
	}

	return openai.Config{
		APIKey:             serviceConfig.APIKey,
		APIURL:             serviceConfig.APIURL,
		OrgID:              serviceConfig.OrgID,
		DefaultModel:       serviceConfig.DefaultModel,
		InputTokenLimit:    serviceConfig.InputTokenLimit,
		OutputTokenLimit:   serviceConfig.OutputTokenLimit,
		StreamingTimeout:   streamingTimeout,
		SendUserID:         serviceConfig.SendUserID,
		UseResponsesAPI:    serviceConfig.UseResponsesAPI,
		EnabledNativeTools: botConfig.EnabledNativeTools,
		ReasoningEnabled:   botConfig.ReasoningEnabled,
		ReasoningEffort:    botConfig.ReasoningEffort,
	}
}

// OpenAIConfigFromServiceConfigWithOptions creates an OpenAI config with additional options for OpenAI-compatible APIs
func OpenAIConfigFromServiceConfigWithOptions(serviceConfig llm.ServiceConfig, botConfig llm.BotConfig, disableStreamOptions bool, useMaxTokens bool) openai.Config {
	cfg := OpenAIConfigFromServiceConfig(serviceConfig, botConfig)
	cfg.DisableStreamOptions = disableStreamOptions
	cfg.UseMaxTokens = useMaxTokens
	// OpenAI-compatible APIs typically don't support the 'user' parameter
	cfg.SendUserID = false
	return cfg
}
