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
//   - providerServices: Provider-side capabilities resolved from the concrete
//     provider client before it was wrapped (see llm.ProviderServices)
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

// ProviderServices returns the provider-side capabilities available to this
// bot's tools, or nil when none were resolved (bots built outside EnsureBots,
// and providers with no such capabilities). Callers must tolerate nil; the
// llm.ProviderServices predicates are nil-safe.
func (b *Bot) ProviderServices() *llm.ProviderServices {
	return b.providerServices
}

// WithConfig returns a copy of the bot with its configuration replaced,
// carrying over the resolved service, Mattermost bot user, language model and
// provider services. Use this instead of re-calling NewBot when deriving a bot
// for a narrower flow, so newly added dependencies are preserved by default
// rather than silently dropped.
func (b *Bot) WithConfig(cfg llm.BotConfig) *Bot {
	if b == nil {
		return nil
	}
	derived := *b
	derived.cfg = cfg
	return &derived
}

// hasNativeToolEnabled reports whether the named native (provider-executed)
// tool is both enabled on the bot and actually deliverable to the resolved
// service's provider. The effective provider capability has to be considered
// rather than trusting the persisted bot config alone: callers use these
// predicates to decide whether to suppress a built-in Mattermost fallback or
// to catalog a companion tool, and a native tool that gets stripped before the
// request would leave them with neither.
func (b *Bot) hasNativeToolEnabled(name string) bool {
	if !bifrost.SupportsNativeTools(b.service.Type) {
		return false
	}
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

// HasNativeWebSearchEnabled reports whether the bot will use the provider's
// native web search. Callers use it to decide whether to suppress Mattermost's
// built-in web search fallback.
func (b *Bot) HasNativeWebSearchEnabled() bool {
	return b.hasNativeToolEnabled(llm.NativeToolWebSearch)
}

// HasNativeCodeExecutionEnabled reports whether the bot will use the provider's
// code-execution sandbox. This says nothing about whether the files that
// sandbox produces can be retrieved — that is a separate provider capability,
// reported by llm.ProviderServices.CanDownloadFiles. Callers that bridge
// sandbox output into Mattermost want SandboxFileAttachmentAvailable instead.
func (b *Bot) HasNativeCodeExecutionEnabled() bool {
	return b.hasNativeToolEnabled(llm.NativeToolCodeInterpreter)
}

// SandboxFileAttachmentAvailable reports whether files this bot's code-execution
// sandbox captures can be attached to its replies. Both halves are required and
// they are independent: the agent has to have the sandbox enabled, and the
// provider has to be able to serve the files it produced (Anthropic can; OpenAI
// container files are not retrievable yet). Both the prompt text that tells the
// model attachment happens and the runtime attach path read this, so they cannot
// disagree.
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

// NewBot creates a new Bot instance. The result has no provider services; use
// EnsureBots for a fully wired bot, or WithConfig to derive one from it.
func NewBot(cfg llm.BotConfig, service llm.ServiceConfig, mmBot *model.Bot, llmInstance llm.LanguageModel) *Bot {
	return &Bot{
		cfg:     cfg,
		service: service,
		mmBot:   mmBot,
		llm:     llmInstance,
	}
}
