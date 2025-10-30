// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/indexer"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/meetings"
	"github.com/mattermost/mattermost-plugin-ai/metrics"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/search"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	ContextPostKey    = "post"
	ContextChannelKey = "channel"
	ContextBotKey     = "bot"
)

type Config interface {
	GetDefaultBotName() string
	MCP() mcp.Config
	AllowUnsafeLinks() bool
}

type MCPClientManager interface {
	GetOAuthManager() *mcp.OAuthManager
	GetToolsCache() *mcp.ToolsCache
	ProcessOAuthCallback(ctx context.Context, loggedInUserID, state, code string) (*mcp.OAuthSession, error)
	GetEmbeddedServer() mcp.EmbeddedMCPServer
}

// API represents the HTTP API functionality for the plugin
type API struct {
	bots                 *bots.MMBots
	conversationsService *conversations.Conversations
	meetingsService      *meetings.Service
	indexerService       *indexer.Indexer
	searchService        *search.Search
	pluginAPI            *pluginapi.Client
	metricsService       metrics.Metrics
	metricsHandler       http.Handler
	contextBuilder       *llmcontext.Builder
	prompts              *llm.Prompts
	config               Config
	mmClient             mmapi.Client
	dbClient             *mmapi.DBClient
	licenseChecker       *enterprise.LicenseChecker
	streamingService     streaming.Service
	i18nBundle           *i18n.Bundle
	mcpClientManager     MCPClientManager
}

// New creates a new API instance
func New(
	bots *bots.MMBots,
	conversationsService *conversations.Conversations,
	meetingsService *meetings.Service,
	indexerService *indexer.Indexer,
	searchService *search.Search,
	pluginAPI *pluginapi.Client,
	metricsService metrics.Metrics,
	llmContextBuilder *llmcontext.Builder,
	config Config,
	prompts *llm.Prompts,
	mmClient mmapi.Client,
	dbClient *mmapi.DBClient,
	licenseChecker *enterprise.LicenseChecker,
	streamingService streaming.Service,
	i18nBundle *i18n.Bundle,
	mcpClientManager MCPClientManager,
) *API {
	return &API{
		bots:                 bots,
		conversationsService: conversationsService,
		meetingsService:      meetingsService,
		indexerService:       indexerService,
		searchService:        searchService,
		pluginAPI:            pluginAPI,
		metricsService:       metricsService,
		metricsHandler:       metrics.NewMetricsHandler(metricsService),
		contextBuilder:       llmContextBuilder,
		prompts:              prompts,
		config:               config,
		mmClient:             mmClient,
		dbClient:             dbClient,
		licenseChecker:       licenseChecker,
		streamingService:     streamingService,
		i18nBundle:           i18nBundle,
		mcpClientManager:     mcpClientManager,
	}
}

// ServeHTTP handles HTTP requests to the plugin
func (a *API) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	router := gin.Default()
	router.Use(a.ginlogger)
	router.Use(a.metricsMiddleware)

	// LLM Bridge API v1 routes - inter-plugin only
	llmBridgeRoute := router.Group("/bridge/v1")
	llmBridgeRoute.Use(a.interPluginAuthorizationRequired)

	// Discovery endpoints
	llmBridgeRoute.GET("/agents", a.handleGetAgents)
	llmBridgeRoute.GET("/services", a.handleGetServices)

	// Completion endpoints
	completionRoute := llmBridgeRoute.Group("/completion")
	completionRoute.POST("/agent/:agent", a.handleAgentCompletionStreaming)
	completionRoute.POST("/agent/:agent/nostream", a.handleAgentCompletionNoStream)
	completionRoute.POST("/service/:service", a.handleServiceCompletionStreaming)
	completionRoute.POST("/service/:service/nostream", a.handleServiceCompletionNoStream)

	router.Use(a.MattermostAuthorizationRequired)

	router.GET("/oauth/callback", a.handleOAuthCallback)
	router.GET("/ai_threads", a.handleGetAIThreads)
	router.GET("/ai_bots", a.handleGetAIBots)

	botRequiredRouter := router.Group("")
	botRequiredRouter.Use(a.aiBotRequired)

	postRouter := botRequiredRouter.Group("/post/:postid")
	postRouter.Use(a.postAuthorizationRequired)
	postRouter.POST("/react", a.handleReact)
	postRouter.POST("/analyze", a.handleThreadAnalysis)
	postRouter.POST("/transcribe/file/:fileid", a.handleTranscribeFile)
	postRouter.POST("/summarize_transcription", a.handleSummarizeTranscription)
	postRouter.POST("/stop", a.handleStop)
	postRouter.POST("/regenerate", a.handleRegenerate)
	postRouter.POST("/tool_call", a.handleToolCall)
	postRouter.POST("/postback_summary", a.handlePostbackSummary)

	channelRouter := botRequiredRouter.Group("/channel/:channelid")
	channelRouter.Use(a.channelAuthorizationRequired)
	channelRouter.POST("/interval", a.handleInterval)

	adminRouter := router.Group("/admin")
	adminRouter.Use(a.mattermostAdminAuthorizationRequired)
	adminRouter.POST("/reindex", a.handleReindexPosts)
	adminRouter.GET("/reindex/status", a.handleGetJobStatus)
	adminRouter.POST("/reindex/cancel", a.handleCancelJob)
	adminRouter.GET("/mcp/tools", a.handleGetMCPTools)
	adminRouter.POST("/mcp/tools/cache/clear", a.handleClearMCPToolsCache)

	searchRouter := botRequiredRouter.Group("/search")
	// Only returns search results
	searchRouter.POST("", a.handleSearchQuery)
	// Initiates a search and responds to the user in a DM with the selected bot
	searchRouter.POST("/run", a.handleRunSearch)

	router.ServeHTTP(w, r)
}

// ServeMetrics serves the metrics endpoint
func (a *API) ServeMetrics(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	a.metricsHandler.ServeHTTP(w, r)
}

func (a *API) metricsMiddleware(c *gin.Context) {
	a.metricsService.IncrementHTTPRequests()
	now := time.Now()

	c.Next()

	elapsed := float64(time.Since(now)) / float64(time.Second)

	status := c.Writer.Status()

	if status < 200 || status > 299 {
		a.metricsService.IncrementHTTPErrors()
	}

	endpoint := c.HandlerName()
	a.metricsService.ObserveAPIEndpointDuration(endpoint, c.Request.Method, strconv.Itoa(status), elapsed)
}

func (a *API) aiBotRequired(c *gin.Context) {
	// We should integreate LLM here
	botUsername := c.Query("botUsername")
	bot := a.bots.GetBotByUsernameOrFirst(botUsername)
	if bot == nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get bot: %s", botUsername))
		return
	}
	c.Set(ContextBotKey, bot)
}

func (a *API) ginlogger(c *gin.Context) {
	c.Next()

	for _, ginErr := range c.Errors {
		a.pluginAPI.Log.Error(ginErr.Error())
	}
}

func (a *API) MattermostAuthorizationRequired(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	if userID == "" {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
}

func (a *API) interPluginAuthorizationRequired(c *gin.Context) {
	pluginID := c.GetHeader("Mattermost-Plugin-ID")
	if pluginID != "" {
		return
	}
	c.AbortWithStatus(http.StatusUnauthorized)
}

// enforceEmptyBody checks if the request body is empty returning an error if not
func (a *API) enforceEmptyBody(c *gin.Context) error {
	// Check the body is empty
	if _, err := c.Request.Body.Read(make([]byte, 1)); err != io.EOF {
		return fmt.Errorf("request body must be empty")
	}
	return nil
}

func (a *API) handleGetAIThreads(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	threads, err := a.conversationsService.GetAIThreads(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get posts for bot DM: %w", err))
		return
	}

	c.JSON(http.StatusOK, threads)
}

type AIBotInfo struct {
	ID                 string                 `json:"id"`
	DisplayName        string                 `json:"displayName"`
	Username           string                 `json:"username"`
	LastIconUpdate     int64                  `json:"lastIconUpdate"`
	DMChannelID        string                 `json:"dmChannelID"`
	ChannelAccessLevel llm.ChannelAccessLevel `json:"channelAccessLevel"`
	ChannelIDs         []string               `json:"channelIDs"`
	UserAccessLevel    llm.UserAccessLevel    `json:"userAccessLevel"`
	UserIDs            []string               `json:"userIDs"`
}

type AIBotsResponse struct {
	Bots             []AIBotInfo `json:"bots"`
	SearchEnabled    bool        `json:"searchEnabled"`
	AllowUnsafeLinks bool        `json:"allowUnsafeLinks"`
}

// getAIBotsForUser returns all AI bots available to a user
func (a *API) getAIBotsForUser(userID string) ([]AIBotInfo, error) {
	allBots := a.bots.GetAllBots()

	// Get the info from all the bots.
	// Put the default bot first.
	bots := make([]AIBotInfo, 0, len(allBots))
	defaultBotName := a.config.GetDefaultBotName()
	for i, bot := range allBots {
		// Don't return bots the user is excluded from using.
		if a.bots.CheckUsageRestrictionsForUser(bot, userID) != nil {
			continue
		}

		// Get the bot DM channel ID. To avoid creating the channel unless nessary
		/// we return "" if the channel doesn't exist.
		dmChannelID := ""
		channelName := model.GetDMNameFromIds(userID, bot.GetMMBot().UserId)
		botDMChannel, err := a.pluginAPI.Channel.GetByName("", channelName, false)
		if err == nil {
			dmChannelID = botDMChannel.Id
		}

		bots = append(bots, AIBotInfo{
			ID:                 bot.GetMMBot().UserId,
			DisplayName:        bot.GetMMBot().DisplayName,
			Username:           bot.GetMMBot().Username,
			LastIconUpdate:     bot.GetMMBot().LastIconUpdate,
			DMChannelID:        dmChannelID,
			ChannelAccessLevel: bot.GetConfig().ChannelAccessLevel,
			ChannelIDs:         bot.GetConfig().ChannelIDs,
			UserAccessLevel:    bot.GetConfig().UserAccessLevel,
			UserIDs:            bot.GetConfig().UserIDs,
		})
		if bot.GetMMBot().Username == defaultBotName {
			bots[0], bots[i] = bots[i], bots[0]
		}
	}

	return bots, nil
}

func (a *API) handleGetAIBots(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	bots, err := a.getAIBotsForUser(userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Check if search is enabled
	searchEnabled := a.searchService.Enabled()

	c.JSON(http.StatusOK, AIBotsResponse{
		Bots:             bots,
		SearchEnabled:    searchEnabled,
		AllowUnsafeLinks: a.config.AllowUnsafeLinks(),
	})
}
