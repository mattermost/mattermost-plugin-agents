// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	cacheKeyPrefix     = "mcp_tools_cache_v1_"
	cacheGenerationKey = "mcp_tools_cache_generation"
	cacheTTL           = 8 * 24 * time.Hour // 8 days

	listKeysPageSize = 1000
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
	ListKeys(page, count int, options ...pluginapi.ListKeysOption) ([]string, error)
}

// Logger interface for logging operations
type Logger interface {
	Debug(msg string, keyValuePairs ...any)
	Info(msg string, keyValuePairs ...any)
	Warn(msg string, keyValuePairs ...any)
	Error(msg string, keyValuePairs ...any)
}

// ToolsCache manages the global cache of MCP tools across all users
// It uses the KV store directly for HA mode compatibility
type ToolsCache struct {
	kvAPI KVStore
	log   Logger
	// generation binds a connection to the cache generation it discovered.
	// Nil selects the current shared generation for standalone cache operations.
	generation *string
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
	key, err := tc.cacheKey(serverID)
	if err != nil {
		tc.log.Error("Failed to read tools cache generation", "error", err)
		return nil
	}

	var cached CachedTools
	err = tc.kvAPI.Get(key, &cached)
	if err != nil {
		tc.log.Error("Failed to retrieve tools cache from KV store", "serverID", serverID, "error", err)
		return nil
	}

	// Get() returns no error for non-existent keys, just doesn't populate the struct
	// Check if data was actually populated
	if cached.Tools == nil || cached.Timestamp.IsZero() {
		tc.log.Debug("Cache miss for server", "serverID", serverID)
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
	// Set returns (false, err) if DB error occurred
	// Set returns (false, nil) if the value was not set
	// Set returns (true, nil) if the value was set
	key, err := tc.cacheKey(serverID)
	if err != nil {
		return err
	}
	success, err := tc.kvAPI.Set(key, cached, pluginapi.SetExpiry(cacheTTL))
	if err != nil {
		tc.log.Error("Failed to persist tools cache to KV store (DB error)", "serverID", serverID, "error", err)
		return fmt.Errorf("failed to persist cache: %w", err)
	}
	if !success {
		tc.log.Warn("Tools cache was not persisted to KV store", "serverID", serverID)
		return fmt.Errorf("cache was not persisted for server %s", serverID)
	}

	tc.log.Debug("Updated tools cache for server", "serverID", serverID, "tools", len(tools))
	return nil
}

// InvalidateServer removes a server's cache entry from KV store
// Delete returns no error for non-existent keys, only errors on actual failures
func (tc *ToolsCache) InvalidateServer(serverID string) error {
	key, err := tc.cacheKey(serverID)
	if err != nil {
		return err
	}
	err = tc.kvAPI.Delete(key)
	if err != nil {
		tc.log.Error("Failed to delete tools cache from KV store", "serverID", serverID, "error", err)
		return fmt.Errorf("failed to delete cache: %w", err)
	}

	tc.log.Debug("Invalidated tools cache for server (deleted if existed)", "serverID", serverID)
	return nil
}

// ClearAll invalidates cached tools and removes entries from older generations.
func (tc *ToolsCache) ClearAll() (int, error) {
	tc.log.Info("Clearing all MCP tools cache entries")

	// Publish a new namespace before deleting old entries. A discovery already
	// in flight retains its old namespace, including on another cluster node.
	generation := model.NewId()
	saved, err := tc.kvAPI.Set(cacheGenerationKey, generation)
	if err != nil {
		return 0, fmt.Errorf("failed to rotate tools cache generation: %w", err)
	}
	if !saved {
		return 0, fmt.Errorf("tools cache generation was not persisted")
	}
	currentPrefix := cacheKeyPrefix + generation + "_"

	// The KV store has no server-side prefix filter, so every page is listed.
	// Keys are collected before deleting so deletions do not shift later pages.
	var keys []string
	for page := 0; ; page++ {
		pageKeys, err := tc.kvAPI.ListKeys(page, listKeysPageSize)
		if err != nil {
			tc.log.Error("Failed to list cache keys from KV store", "error", err)
			return 0, fmt.Errorf("failed to list cache keys: %w", err)
		}
		for _, key := range pageKeys {
			if strings.HasPrefix(key, cacheKeyPrefix) && !strings.HasPrefix(key, currentPrefix) {
				keys = append(keys, key)
			}
		}
		if len(pageKeys) < listKeysPageSize {
			break
		}
	}

	clearedCount := 0
	var clearErr error
	for _, key := range keys {
		if err := tc.kvAPI.Delete(key); err != nil {
			tc.log.Warn("Failed to delete cache key", "key", key, "error", err)
			clearErr = errors.Join(clearErr, fmt.Errorf("failed to delete cache: %w", err))
			continue
		}
		clearedCount++
	}

	tc.log.Info("Cleared MCP tools cache", "clearedCount", clearedCount)
	return clearedCount, clearErr
}

// snapshot binds cache reads and writes to one shared generation. Old
// connections cannot repopulate the namespace selected by a later clear.
func (tc *ToolsCache) snapshot() (*ToolsCache, error) {
	if tc.generation != nil {
		return tc, nil
	}
	var generation string
	if err := tc.kvAPI.Get(cacheGenerationKey, &generation); err != nil && !errors.Is(err, pluginapi.ErrNotFound) {
		return nil, fmt.Errorf("failed to read tools cache generation: %w", err)
	}
	return &ToolsCache{kvAPI: tc.kvAPI, log: tc.log, generation: &generation}, nil
}

func (tc *ToolsCache) cacheKey(serverID string) (string, error) {
	snapshot, err := tc.snapshot()
	if err != nil {
		return "", err
	}
	if *snapshot.generation == "" {
		return tc.buildCacheKey(serverID), nil
	}
	return cacheKeyPrefix + *snapshot.generation + "_" + serverID, nil
}

// buildCacheKey constructs the legacy KV key for a server.
func (tc *ToolsCache) buildCacheKey(serverID string) string {
	return cacheKeyPrefix + serverID
}
