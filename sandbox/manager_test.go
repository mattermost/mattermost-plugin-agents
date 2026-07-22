// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

func freeListenAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func TestManagerApplyAndClose(t *testing.T) {
	addr := freeListenAddr(t)
	var apps atomic.Value
	apps.Store(config.MCPAppsConfig{})

	m := NewManager(func() (config.MCPAppsConfig, string) {
		return apps.Load().(config.MCPAppsConfig), "https://mm.example.com"
	}, noopLogger{}, nil)

	m.ApplyCurrent()
	require.False(t, m.Running())

	apps.Store(config.MCPAppsConfig{
		Enabled:              true,
		SandboxURL:           "https://apps.example.com",
		SandboxListenAddress: addr,
	})
	m.ApplyCurrent()
	require.True(t, m.Running())

	// Duplicate apply is a no-op (still running).
	m.ApplyCurrent()
	require.True(t, m.Running())

	apps.Store(config.MCPAppsConfig{Enabled: false})
	m.ApplyCurrent()
	require.False(t, m.Running())

	// Use a fresh port after shutdown to avoid ephemeral-port reuse races.
	addr2 := freeListenAddr(t)
	apps.Store(config.MCPAppsConfig{
		Enabled:              true,
		SandboxURL:           "https://apps.example.com",
		SandboxListenAddress: addr2,
	})
	m.ApplyCurrent()
	require.True(t, m.Running())

	m.Close()
	require.False(t, m.Running())

	// Late apply after Close must not restart.
	m.ApplyCurrent()
	require.False(t, m.Running())
}

func TestManagerPortConflictLogAndDisable(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer held.Close()
	addr := held.Addr().String()

	m := NewManager(func() (config.MCPAppsConfig, string) {
		return config.MCPAppsConfig{
			Enabled:              true,
			SandboxURL:           "https://apps.example.com",
			SandboxListenAddress: addr,
		}, "https://mm.example.com"
	}, noopLogger{}, nil)

	m.ApplyCurrent()
	require.False(t, m.Running(), "port conflict must log-and-disable")
}

func TestManagerConcurrentPortChanges(t *testing.T) {
	ports := []string{freeListenAddr(t), freeListenAddr(t)}
	var mu sync.Mutex
	apps := config.MCPAppsConfig{
		Enabled:    true,
		SandboxURL: "https://apps.example.com",
	}

	m := NewManager(func() (config.MCPAppsConfig, string) {
		mu.Lock()
		defer mu.Unlock()
		return apps, "https://mm.example.com"
	}, noopLogger{}, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mu.Lock()
			apps.SandboxListenAddress = ports[i%2]
			mu.Unlock()
			m.ApplyCurrent()
		}(i)
	}
	wg.Wait()

	if m.Running() {
		addr := m.Addr()
		require.Contains(t, ports, addr)
	}
	m.Close()
	require.False(t, m.Running())
}

func TestManagerApplyVsDeactivate(t *testing.T) {
	addr := freeListenAddr(t)

	m := NewManager(func() (config.MCPAppsConfig, string) {
		return config.MCPAppsConfig{
			Enabled:              true,
			SandboxURL:           "https://apps.example.com",
			SandboxListenAddress: addr,
		}, "https://mm.example.com"
	}, noopLogger{}, nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.ApplyCurrent()
		}()
		go func() {
			defer wg.Done()
			m.Close()
		}()
	}
	wg.Wait()

	require.False(t, m.Running(), "after concurrent apply/close, manager must be stopped")
	m.ApplyCurrent()
	require.False(t, m.Running(), "ApplyCurrent after Close must remain terminal")
}

func TestManagerEnableDisableOrdering(t *testing.T) {
	var apps atomic.Value
	applyEnabled := func() {
		apps.Store(config.MCPAppsConfig{
			Enabled:              true,
			SandboxURL:           "https://apps.example.com",
			SandboxListenAddress: freeListenAddr(t),
		})
	}
	applyEnabled()

	m := NewManager(func() (config.MCPAppsConfig, string) {
		return apps.Load().(config.MCPAppsConfig), "https://mm.example.com"
	}, noopLogger{}, nil)

	m.ApplyCurrent()
	require.True(t, m.Running())

	apps.Store(config.MCPAppsConfig{Enabled: false})
	m.ApplyCurrent()
	require.False(t, m.Running())

	applyEnabled()
	m.ApplyCurrent()
	if !m.Running() {
		// Rare ephemeral-port collision under -race; retry once with a new port.
		applyEnabled()
		m.ApplyCurrent()
	}
	require.True(t, m.Running())
	require.NotEmpty(t, m.Addr())
	m.Close()
}
