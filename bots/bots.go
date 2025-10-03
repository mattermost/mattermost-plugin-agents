// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/mattermost/mattermost-plugin-ai/anthropic"
	"github.com/mattermost/mattermost-plugin-ai/asage"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/openai"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

type Config interface {
	GetDefaultBotName() string
	EnableLLMLogging() bool
	EnableTokenUsageLogging() bool
	GetTranscriptGenerator() string
}

// Transcriber interface defines the contract for transcription services
type Transcriber interface {
	Transcribe(file io.Reader) (*subtitles.Subtitles, error)
}

type MMBots struct {
	ensureBotsClusterMutex cluster.MutexPluginAPI
	pluginAPI              *pluginapi.Client
	licenseChecker         *enterprise.LicenseChecker
	config                 Config
	llmUpstreamHTTPClient  *http.Client
	tokenLogger            *mlog.Logger

	botsLock sync.RWMutex
	bots     []*Bot
}

func New(mutexPluginAPI cluster.MutexPluginAPI, pluginAPI *pluginapi.Client, licenseChecker *enterprise.LicenseChecker, config Config, llmUpstreamHTTPClient *http.Client, tokenLogger *mlog.Logger) *MMBots {
	return &MMBots{
		ensureBotsClusterMutex: mutexPluginAPI,
		pluginAPI:              pluginAPI,
		licenseChecker:         licenseChecker,
		config:                 config,
		llmUpstreamHTTPClient:  llmUpstreamHTTPClient,
		tokenLogger:            tokenLogger,
	}
}

func (b *MMBots) EnsureBots(cfgBots []llm.BotConfig) error {
	mtx, err := cluster.NewMutex(b.ensureBotsClusterMutex, "ai_ensure_bots")
	if err != nil {
		return fmt.Errorf("failed to create mutex: %w", err)
	}
	mtx.Lock()
	defer mtx.Unlock()

	previousMMBots, err := b.pluginAPI.Bot.List(0, 1000, pluginapi.BotOwner("mattermost-ai"), pluginapi.BotIncludeDeleted())
	if err != nil {
		return fmt.Errorf("failed to list bots: %w", err)
	}

	// Only allow one bot if not multi-LLM licensed
	if len(cfgBots) > 1 && !b.licenseChecker.IsMultiLLMLicensed() {
		b.pluginAPI.Log.Error("Only one bot allowed with current license.")
		cfgBots = cfgBots[:1]
	}

	aiBotConfigsByUsername := make(map[string]llm.BotConfig)
	for _, bot := range cfgBots {
		if !bot.IsValid() {
			b.pluginAPI.Log.Error("Configured bot is not valid", "bot_name", bot.Name, "bot_display_name", bot.DisplayName)
			continue
		}
		if _, ok := aiBotConfigsByUsername[bot.Name]; ok {
			// Duplicate bot names have to be fatal because they would cause a bot to be modified inappropreately.
			return fmt.Errorf("duplicate bot name: %s", bot.Name)
		}
		aiBotConfigsByUsername[bot.Name] = bot
	}

	prevousMMBotsByUsername := make(map[string]*model.Bot)
	for _, bot := range previousMMBots {
		prevousMMBotsByUsername[bot.Username] = bot
	}

	// For each of the bots we found, if it's not in the configuration, delete it.
	for _, bot := range previousMMBots {
		if _, ok := aiBotConfigsByUsername[bot.Username]; !ok {
			if _, err := b.pluginAPI.Bot.UpdateActive(bot.UserId, false); err != nil {
				b.pluginAPI.Log.Error("Failed to delete bot", "bot_name", bot.Username, "error", err.Error())
				continue
			}
		}
	}

	// For each bot in the configuration, try to find an existing bot matching the username.
	// If it exists, update it to match. Otherwise, create a new bot.
	for _, bot := range cfgBots {
		if !bot.IsValid() {
			continue
		}
		description := "Powered by " + bot.Service.Type
		if prevBot, ok := prevousMMBotsByUsername[bot.Name]; ok {
			if _, err := b.pluginAPI.Bot.Patch(prevBot.UserId, &model.BotPatch{
				DisplayName: &bot.DisplayName,
				Description: &description,
			}); err != nil {
				b.pluginAPI.Log.Error("Failed to patch bot", "bot_name", bot.Name, "error", err.Error())
				continue
			}
			if _, err := b.pluginAPI.Bot.UpdateActive(prevBot.UserId, true); err != nil {
				b.pluginAPI.Log.Error("Failed to update bot active", "bot_name", bot.Name, "error", err.Error())
				continue
			}
		} else {
			err := b.pluginAPI.Bot.Create(&model.Bot{
				Username:    bot.Name,
				DisplayName: bot.DisplayName,
				Description: description,
			})
			if err != nil {
				b.pluginAPI.Log.Error("Failed to ensure bot", "bot_name", bot.Name, "error", err.Error())
				continue
			}
		}
	}

	if err := b.UpdateBotsCache(cfgBots); err != nil {
		return err
	}

	return nil
}

func (b *MMBots) UpdateBotsCache(cfgBots []llm.BotConfig) error {
	bots, err := b.pluginAPI.Bot.List(0, 1000, pluginapi.BotOwner("mattermost-ai"))
	if err != nil {
		return fmt.Errorf("failed to list bots: %w", err)
	}

	b.botsLock.Lock()
	defer b.botsLock.Unlock()
	b.bots = make([]*Bot, 0, len(cfgBots))
	for _, botCfg := range cfgBots {
		for _, bot := range bots {
			if bot.Username == botCfg.Name {
				createdBot := NewBot(botCfg, bot)
				b.bots = append(b.bots, createdBot)
			}
		}
	}

	for _, bot := range b.bots {
		var err error
		bot.llm, err = b.getLLM(bot.cfg.Service, bot.cfg.Name)
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *MMBots) getLLM(serviceConfig llm.ServiceConfig, botName string) (llm.LanguageModel, error) {
	// Create the correct model
	var result llm.LanguageModel
	switch serviceConfig.Type {
	case llm.ServiceTypeOpenAI:
		result = openai.New(config.OpenAIConfigFromServiceConfig(serviceConfig), b.llmUpstreamHTTPClient)
	case llm.ServiceTypeOpenAICompatible:
		result = openai.NewCompatible(config.OpenAIConfigFromServiceConfig(serviceConfig), b.llmUpstreamHTTPClient)
	case llm.ServiceTypeAzure:
		result = openai.NewAzure(config.OpenAIConfigFromServiceConfig(serviceConfig), b.llmUpstreamHTTPClient)
	case llm.ServiceTypeAnthropic:
		result = anthropic.New(serviceConfig, b.llmUpstreamHTTPClient)
	case llm.ServiceTypeASage:
		result = asage.New(serviceConfig, b.llmUpstreamHTTPClient)
	case llm.ServiceTypeCohere:
		// Set the Cohere OpenAI compatibility endpoint
		cohereCfg := serviceConfig
		cohereCfg.APIURL = "https://api.cohere.ai/compatibility/v1"
		result = openai.NewCompatible(config.OpenAIConfigFromServiceConfig(cohereCfg), b.llmUpstreamHTTPClient)
	}

	// Truncation Support
	result = llm.NewLLMTruncationWrapper(result)

	// Token Usage Logging
	if b.tokenLogger != nil && b.config.EnableTokenUsageLogging() {
		result = llm.NewTokenUsageLoggingWrapper(result, botName, b.tokenLogger)
	}

	// Logging
	if b.config.EnableLLMLogging() {
		result = llm.NewLanguageModelLogWrapper(b.pluginAPI.Log, result)
	}

	return result, nil
}

// TODO: This really doesn't belong here. Figure out where to put this.
func (b *MMBots) GetTranscribe() Transcriber {
	// Get the configured transcript generator bot
	bot := b.getTrasncriberBot()
	if bot == nil {
		b.pluginAPI.Log.Error("No transcript generator bot found")
		return nil
	}

	service := bot.GetConfig().Service
	switch service.Type {
	case llm.ServiceTypeOpenAI:
		return openai.New(config.OpenAIConfigFromServiceConfig(service), b.llmUpstreamHTTPClient)
	case llm.ServiceTypeOpenAICompatible:
		return openai.NewCompatible(config.OpenAIConfigFromServiceConfig(service), b.llmUpstreamHTTPClient)
	case llm.ServiceTypeAzure:
		return openai.NewAzure(config.OpenAIConfigFromServiceConfig(service), b.llmUpstreamHTTPClient)
	default:
		b.pluginAPI.Log.Error("Unsupported service type for transcript generator",
			"bot_name", bot.GetMMBot().Username,
			"service_type", service.Type)
		return nil
	}
}

func (b *MMBots) getTrasncriberBot() *Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()

	for _, bot := range b.bots {
		if bot.cfg.Name == b.config.GetTranscriptGenerator() {
			return bot
		}
	}

	return nil
}

func (b *MMBots) GetBotConfig(botUsername string) (llm.BotConfig, error) {
	bot := b.GetBotByUsername(botUsername)
	if bot == nil {
		return llm.BotConfig{}, fmt.Errorf("bot not found")
	}

	return bot.cfg, nil
}

// GetBotByUsername retrieves the bot associated with the given bot username
func (b *MMBots) GetBotByUsername(botUsername string) *Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()
	for _, bot := range b.bots {
		if bot.cfg.Name == botUsername {
			return bot
		}
	}

	return nil
}

// GetBotByUsernameOrFirst retrieves the bot associated with the given bot username or the first bot if not found
func (b *MMBots) GetBotByUsernameOrFirst(botUsername string) *Bot {
	bot := b.GetBotByUsername(botUsername)
	if bot != nil {
		return bot
	}

	b.botsLock.RLock()
	defer b.botsLock.RUnlock()
	if len(b.bots) > 0 {
		return b.bots[0]
	}

	return nil
}

// GetBotByID retrieves the bot associated with the given bot ID
func (b *MMBots) GetBotByID(botID string) *Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()
	for _, bot := range b.bots {
		if bot.mmBot.UserId == botID {
			return bot
		}
	}

	return nil
}

// GetBotForDMChannel returns the bot for the given DM channel.
func (b *MMBots) GetBotForDMChannel(channel *model.Channel) *Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()

	for _, bot := range b.bots {
		if mmapi.IsDMWith(bot.mmBot.UserId, channel) {
			return bot
		}
	}
	return nil
}

// IsAnyBot returns true if the given user is an AI bot.
func (b *MMBots) IsAnyBot(userID string) bool {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()
	for _, bot := range b.bots {
		if bot.mmBot.UserId == userID {
			return true
		}
	}

	return false
}

// GetBotMentioned returns the bot mentioned in the text, if any.
func (b *MMBots) GetBotMentioned(text string) *Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()

	for _, bot := range b.bots {
		if userIsMentionedMarkdown(text, bot.mmBot.Username) {
			return bot
		}
	}

	return nil
}

// GetAllBots returns all bots
func (b *MMBots) GetAllBots() []*Bot {
	b.botsLock.RLock()
	defer b.botsLock.RUnlock()

	return b.bots
}

// SetBotsForTesting sets bots directly for testing purposes only
func (b *MMBots) SetBotsForTesting(bots []*Bot) {
	b.botsLock.Lock()
	defer b.botsLock.Unlock()
	b.bots = bots
}
