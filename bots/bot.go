// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"github.com/mattermost/mattermost-plugin-ai/llm"
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
//
// Bot instances should be created via EnsureBots() which properly resolves
// service references and initializes all fields.
type Bot struct {
	cfg     llm.BotConfig
	service llm.ServiceConfig
	mmBot   *model.Bot
	llm     llm.LanguageModel
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

func (b *Bot) SetLLMForTest(llm llm.LanguageModel) {
	b.llm = llm
}

func (b *Bot) SetServiceForTest(service llm.ServiceConfig) {
	b.service = service
}

// NewBot creates a new Bot instance with all fields initialized.
func NewBot(cfg llm.BotConfig, service llm.ServiceConfig, mmBot *model.Bot, llmInstance llm.LanguageModel) *Bot {
	return &Bot{
		cfg:     cfg,
		service: service,
		mmBot:   mmBot,
		llm:     llmInstance,
	}
}
