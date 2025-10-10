// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	cacheKeyPrefix = "mcp_tools_cache_v1_"
	cacheTTL       = 8 * 24 * time.Hour // 8 days
)

// CachedTools represents a cached set of tools for a specific MCP server
type CachedTools struct {
	Tools      map[string]*mcp.Tool `json:"tools"`
	Timestamp  time.Time            `json:"timestamp"`
	ServerURL  string               `json:"server_url"`
	ServerName string               `json:"server_name"`
}

// KVStore interface for key-value operations
type KVStore interface {
	Get(key string, o any) error
	Set(key string, value any, options ...pluginapi.KVSetOption) (bool, error)
	Delete(key string) error
}

// Logger interface for logging operations (allows mocking)
type Logger interface {
	Debug(msg string, keyValuePairs ...interface{})
	Info(msg string, keyValuePairs ...interface{})
	Warn(msg string, keyValuePairs ...interface{})
	Error(msg string, keyValuePairs ...interface{})
}

// ToolsCache manages the global cache of MCP tools across all users
// It uses the KV store directly for HA mode compatibility
type ToolsCache struct {
	kvAPI KVStore
	log   Logger
}

// NewToolsCache creates a new ToolsCache instance
func NewToolsCache(kvAPI KVStore, log Logger) *ToolsCache {
	return &ToolsCache{
		kvAPI: kvAPI,
		log:   log,
	}
}

// GetTools retrieves cached tools for a server, returns nil if missing
func (tc *ToolsCache) GetTools(serverID string) map[string]*mcp.Tool {
	key := tc.buildCacheKey(serverID)

	var cached CachedTools
	if err := tc.kvAPI.Get(key, &cached); err != nil {
		tc.log.Debug("Cache miss for server", "serverID", serverID, "error", err)
		return nil
	}

	tc.log.Debug("Cache hit for server", "serverID", serverID, "tools", len(cached.Tools))
	return cached.Tools
}

// SetTools updates the cache for a server and persists to KV store
func (tc *ToolsCache) SetTools(serverID string, serverName string, serverURL string, tools map[string]*mcp.Tool, timestamp time.Time) error {
	cached := &CachedTools{
		Tools:      tools,
		Timestamp:  timestamp,
		ServerURL:  serverURL,
		ServerName: serverName,
	}

	// Persist directly to KV store
	key := tc.buildCacheKey(serverID)
	_, err := tc.kvAPI.Set(key, cached, pluginapi.SetExpiry(cacheTTL))
	if err != nil {
		tc.log.Error("Failed to persist tools cache to KV store", "serverID", serverID, "error", err)
		return fmt.Errorf("failed to persist cache: %w", err)
	}

	tc.log.Debug("Updated tools cache for server", "serverID", serverID, "tools", len(tools))
	return nil
}

// InvalidateServer removes a server's cache entry from KV store
func (tc *ToolsCache) InvalidateServer(serverID string) error {
	key := tc.buildCacheKey(serverID)
	if err := tc.kvAPI.Delete(key); err != nil {
		tc.log.Error("Failed to delete tools cache from KV store", "serverID", serverID, "error", err)
		return fmt.Errorf("failed to delete cache: %w", err)
	}

	tc.log.Debug("Invalidated tools cache for server", "serverID", serverID)
	return nil
}

// buildCacheKey constructs the KV store key for a server
func (tc *ToolsCache) buildCacheKey(serverID string) string {
	return cacheKeyPrefix + serverID
}
