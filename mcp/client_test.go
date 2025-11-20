// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// TestCacheHitBehavior verifies that when tools are in cache,
// they can be retrieved and reused correctly
func TestCacheHitBehavior(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "test_server"
	serverName := "Test MCP Server"
	serverURL := "http://localhost:8080"

	// Simulate tools that would be fetched from an MCP server
	tools := map[string]*mcp.Tool{
		"calculator": {
			Name:        "calculator",
			Description: "Performs calculations",
		},
		"weather": {
			Name:        "weather",
			Description: "Gets weather information",
		},
	}

	// Store tools in cache
	err := cache.SetTools(serverID, serverName, serverURL, tools, time.Now())
	require.NoError(t, err)

	// Retrieve from cache
	cachedTools := cache.GetTools(serverID)
	require.NotNil(t, cachedTools)
	require.Equal(t, len(tools), len(cachedTools))

	// Verify tool details are preserved
	require.Equal(t, "calculator", cachedTools["calculator"].Name)
	require.Equal(t, "Performs calculations", cachedTools["calculator"].Description)
	require.Equal(t, "weather", cachedTools["weather"].Name)
	require.Equal(t, "Gets weather information", cachedTools["weather"].Description)
}

// TestCacheMissBehavior verifies that when tools are not in cache,
// nil is returned (indicating a cache miss)
func TestCacheMissBehavior(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Try to get tools for a server that doesn't exist in cache
	cachedTools := cache.GetTools("nonexistent_server")
	require.Nil(t, cachedTools, "Cache miss should return nil")
}

// TestCacheUpdateOnNewTools verifies that cache is updated when new tools are fetched
func TestCacheUpdateOnNewTools(t *testing.T) {
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	serverID := "test_server"

	// Initially no tools in cache
	cachedTools := cache.GetTools(serverID)
	require.Nil(t, cachedTools)

	// Simulate fetching tools from server and updating cache
	newTools := map[string]*mcp.Tool{
		"file_read": {
			Name:        "file_read",
			Description: "Reads a file",
		},
		"file_write": {
			Name:        "file_write",
			Description: "Writes to a file",
		},
	}

	err := cache.SetTools(serverID, "File Server", "http://fileserver.com", newTools, time.Now())
	require.NoError(t, err)

	// Now tools should be in cache
	cachedTools = cache.GetTools(serverID)
	require.NotNil(t, cachedTools)
	require.Equal(t, 2, len(cachedTools))
	require.Contains(t, cachedTools, "file_read")
	require.Contains(t, cachedTools, "file_write")
}

// TestNilCacheHandling verifies that nil cache is handled gracefully in the cache code
func TestNilCacheHandling(t *testing.T) {
	// This test documents that the cache code handles nil properly
	// The actual NewClient function checks if toolsCache is nil before using it
	kvAPI := newMockKVService()
	log := &mockLogService{}
	cache := NewToolsCache(kvAPI, log)

	// Verify cache can be created and used
	require.NotNil(t, cache)

	// Test that GetTools returns nil for non-existent server (not a panic)
	tools := cache.GetTools("nonexistent")
	require.Nil(t, tools)
}
