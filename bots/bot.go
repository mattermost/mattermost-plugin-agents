// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// Bot represents an AI bot instance with its configuration and dependencies.
//
// Source of truth for bot fields:
//   - cfg: The bot's configuration (name, display name, permissions, etc.)
//   - service: The RESOLVED service configuration (use GetService() to access).
//     DO NOT use cfg.Service or cfg.ServiceID directly - those are internal references.
//   - mmBot: The Mattermost bot user
//   - llm: The initialized language model instance
//   - providerServices: resolved from the concrete client before wrapping (see llm.ProviderServices)
//
// Bot instances should be created via EnsureBots() which properly resolves
// service references and initializes all fields.
type Bot struct {
	cfg              llm.BotConfig
	service          llm.ServiceConfig
	mmBot            *model.Bot
	llm              llm.LanguageModel
	providerServices *llm.ProviderServices
}

func (b *Bot) GetConfig() llm.BotConfig {
	return b.cfg
}

func (b *Bot) GetMMBot() *model.Bot {
	return b.mmBot
}

func (b *Bot) LLM() llm.LanguageModel {
	return b.llm
}

func (b *Bot) GetService() llm.ServiceConfig {
	return b.service
}

// ProviderServices is nil-safe; callers must tolerate nil (bots built outside EnsureBots).
func (b *Bot) ProviderServices() *llm.ProviderServices {
	return b.providerServices
}

// WithConfig copies the bot with a new config. Use this instead of NewBot so
// resolved LLM and provider services are not silently dropped.
func (b *Bot) WithConfig(cfg llm.BotConfig) *Bot {
	if b == nil {
		return nil
	}
	derived := *b
	derived.cfg = cfg
	return &derived
}

// hasNativeToolEnabled is true only if the tool is enabled and the resolved
// provider can actually deliver it. Callers use this to suppress Mattermost
// fallbacks; trusting persisted config alone would leave them with neither.
func (b *Bot) hasNativeToolEnabled(name string) bool {
	// SupportedNativeToolsForServiceType is the single source of truth for
	// which providers deliver native tools; it is empty for the rest.
	if !slices.Contains(bifrost.SupportedNativeToolsForServiceType(b.service.Type), name) {
		return false
	}
	switch b.service.Type {
	case llm.ServiceTypeOpenAICompatible, llm.ServiceTypeAzure:
		// Native tools only reach these providers over the Responses API.
		if !llm.ServiceUsesResponsesAPI(b.service) {
			return false
		}
	}
	return slices.Contains(b.cfg.EnabledNativeTools, name)
}

// HasNativeWebSearchEnabled is used to suppress Mattermost's built-in web search fallback.
func (b *Bot) HasNativeWebSearchEnabled() bool {
	return b.hasNativeToolEnabled(llm.NativeToolWebSearch)
}

// HasNativeCodeExecutionEnabled is independent of file retrieval. OpenAI can
// run the sandbox but cannot serve its files; use SandboxFileAttachmentAvailable
// for attach/prompt gating.
func (b *Bot) HasNativeCodeExecutionEnabled() bool {
	return b.hasNativeToolEnabled(llm.NativeToolCodeInterpreter)
}

// SandboxFileAttachmentAvailable requires both the sandbox and a retrievable
// file API. Prompt text and the attach path must use this so they cannot disagree.
func (b *Bot) SandboxFileAttachmentAvailable() bool {
	if b == nil {
		return false
	}
	return b.HasNativeCodeExecutionEnabled() && b.ProviderServices().CanDownloadFiles()
}

func (b *Bot) SetLLMForTest(llm llm.LanguageModel) {
	b.llm = llm
}

func (b *Bot) SetServiceForTest(service llm.ServiceConfig) {
	b.service = service
}

func (b *Bot) SetProviderServicesForTest(services *llm.ProviderServices) {
	b.providerServices = services
}

// NewBot has no provider services. Use EnsureBots for a wired bot, or WithConfig to derive one.
func NewBot(cfg llm.BotConfig, service llm.ServiceConfig, mmBot *model.Bot, llmInstance llm.LanguageModel) *Bot {
	return &Bot{
		cfg:     cfg,
		service: service,
		mmBot:   mmBot,
		llm:     llmInstance,
	}
}
