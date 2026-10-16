// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// mockKVService implements pluginapi.KVService for testing with thread-safety
type mockKVService struct {
	mu    sync.RWMutex
	store map[string][]byte
}

func newMockKVService() *mockKVService {
	return &mockKVService{
		store: make(map[string][]byte),
	}
}

func (m *mockKVService) Get(key string, o any) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, exists := m.store[key]
	if !exists {
		return pluginapi.ErrNotFound
	}
	return json.Unmarshal(data, o)
}

func (m *mockKVService) Set(key string, value any, options ...pluginapi.KVSetOption) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.store[key] = data
	return true, nil
}

func (m *mockKVService) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.store, key)
	return nil
}

func (m *mockKVService) DeleteAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.store = make(map[string][]byte)
	return nil
}

// ListKeys pages through keys sorted by name, like the Mattermost KV store.
// Options are not supported: pluginapi applies them in memory to a single page.
func (m *mockKVService) ListKeys(page, count int, options ...pluginapi.ListKeysOption) ([]string, error) {
	if len(options) > 0 {
		return nil, errors.New("mockKVService.ListKeys does not support options")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := slices.Sorted(maps.Keys(m.store))
	start := min(page*count, len(keys))
	end := min(start+count, len(keys))
	return keys[start:end], nil
}

// mockLogService implements pluginapi.LogService for testing
type mockLogService struct{}

func (m *mockLogService) Debug(msg string, keyValuePairs ...any) {}
func (m *mockLogService) Info(msg string, keyValuePairs ...any)  {}
func (m *mockLogService) Warn(msg string, keyValuePairs ...any)  {}
func (m *mockLogService) Error(msg string, keyValuePairs ...any) {}
func (m *mockLogService) Flush() error                           { return nil }

func createTestTools() map[string]*mcp.Tool {
	return map[string]*mcp.Tool{
		"test_tool_1": {
			Name:        "test_tool_1",
			Description: "Test tool 1",
		},
		"test_tool_2": {
			Name:        "test_tool_2",
			Description: "Test tool 2",
		},
	}
}

func TestNewToolsCache(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}

	cache := NewToolsCache(kvAPI, log)

	require.NotNil(t, cache)
	require.NotNil(t, cache.kvAPI)
	require.NotNil(t, cache.log)
}

func TestGetTools_CacheHit(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "test_server"
	tools := createTestTools()
	timestamp := time.Now()

	err := cache.SetTools(serverID, "Test Server", "http://test.com", tools, timestamp)
	require.NoError(t, err)

	retrievedTools := cache.GetTools(serverID)
	require.NotNil(t, retrievedTools)
	require.Equal(t, len(tools), len(retrievedTools))
	require.Equal(t, tools["test_tool_1"].Name, retrievedTools["test_tool_1"].Name)
	require.Equal(t, tools["test_tool_2"].Name, retrievedTools["test_tool_2"].Name)
}

func TestGetTools_CacheMiss(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	retrievedTools := cache.GetTools("nonexistent_server")
	require.Nil(t, retrievedTools)
}

func TestSetTools(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "test_server"
	serverName := "Test Server"
	serverURL := "http://test.com"
	tools := createTestTools()
	timestamp := time.Now()

	err := cache.SetTools(serverID, serverName, serverURL, tools, timestamp)
	require.NoError(t, err)

	// Verify KV store persistence
	var storedCache CachedTools
	key := cache.buildCacheKey(serverID)
	err = kvAPI.Get(key, &storedCache)
	require.NoError(t, err)
	require.Equal(t, len(tools), len(storedCache.Tools))
	require.Equal(t, serverName, storedCache.ServerName)
	require.Equal(t, serverURL, storedCache.ServerURL)
	require.Equal(t, timestamp.Unix(), storedCache.Timestamp.Unix())
}

func TestInvalidateServer(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "test_server"
	tools := createTestTools()
	timestamp := time.Now()

	err := cache.SetTools(serverID, "Test Server", "http://test.com", tools, timestamp)
	require.NoError(t, err)

	// Verify it exists
	require.NotNil(t, cache.GetTools(serverID))

	// Invalidate
	err = cache.InvalidateServer(serverID)
	require.NoError(t, err)

	// Verify it's gone from KV store
	require.Nil(t, cache.GetTools(serverID))

	var storedCache CachedTools
	key := cache.buildCacheKey(serverID)
	err = kvAPI.Get(key, &storedCache)
	require.Error(t, err)
}

func TestInvalidateServerMissingKeyIsNoop(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	require.NoError(t, cache.InvalidateServer("missing_server"))
	require.Nil(t, cache.GetTools("missing_server"))
}

func TestBuildCacheKey(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	tests := []struct {
		name     string
		serverID string
		expected string
	}{
		{
			name:     "simple server ID",
			serverID: "server1",
			expected: "mcp_tools_cache_v1_server1",
		},
		{
			name:     "server ID with special chars",
			serverID: "server-test_123",
			expected: "mcp_tools_cache_v1_server-test_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cache.buildCacheKey(tt.serverID)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Test concurrent writes and reads
	done := make(chan bool)

	// Writer goroutines
	for i := range 10 {
		go func(id int) {
			serverID := "server_" + string(rune('0'+id))
			tools := createTestTools()
			_ = cache.SetTools(serverID, "Server", "http://test.com", tools, time.Now())
			done <- true
		}(i)
	}

	// Reader goroutines
	for i := range 10 {
		go func(id int) {
			serverID := "server_" + string(rune('0'+id))
			cache.GetTools(serverID)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for range 20 {
		<-done
	}

	// No assertion needed - test passes if no race conditions occur
}

func TestClearAll(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Add multiple servers to cache
	servers := []string{"server1", "server2", "server3"}
	tools := createTestTools()

	for _, serverID := range servers {
		err := cache.SetTools(serverID, "Test Server", "http://test.com", tools, time.Now())
		require.NoError(t, err)
	}

	// Verify all are cached
	for _, serverID := range servers {
		cachedTools := cache.GetTools(serverID)
		require.NotNil(t, cachedTools)
		require.Equal(t, len(tools), len(cachedTools))
	}

	// Clear all cache
	clearedCount, err := cache.ClearAll()
	require.NoError(t, err)
	require.Equal(t, len(servers), clearedCount)

	// Verify all are gone
	for _, serverID := range servers {
		cachedTools := cache.GetTools(serverID)
		require.Nil(t, cachedTools)
	}
}

func TestClearAllBeyondFirstKeyPage(t *testing.T) {
	tests := []struct {
		name          string
		unrelatedKeys int
	}{
		{name: "cache keys sort after more than a page of other keys", unrelatedKeys: 2500},
		{name: "total key count is an exact multiple of the page size", unrelatedKeys: 2*listKeysPageSize - 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kvAPI := newMockKVService()
			cache := NewToolsCache(kvAPI, &mockLogService{})

			// Per-user OAuth token keys sort before the cache and profile keys.
			for i := range tc.unrelatedKeys {
				_, err := kvAPI.Set(fmt.Sprintf("mcp_oauth_token_v1_user%05d_server", i), "token")
				require.NoError(t, err)
			}
			servers := []string{"server1", "server2"}
			for _, serverID := range servers {
				require.NoError(t, cache.SetTools(serverID, serverID, "http://test.com", createTestTools(), time.Now()))
			}

			clearedCount, err := cache.ClearAll()
			require.NoError(t, err)
			require.Equal(t, len(servers), clearedCount)

			for _, serverID := range servers {
				require.Nil(t, cache.GetTools(serverID))
			}
			require.Len(t, kvAPI.store, tc.unrelatedKeys)
		})
	}
}

func TestClearAllEmpty(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Clear empty cache should return 0
	clearedCount, err := cache.ClearAll()
	require.NoError(t, err)
	require.Equal(t, 0, clearedCount)
}
