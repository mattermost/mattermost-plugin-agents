// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// stoppedPluginCheckInterval matches the server's plugin health check, which
// notices crashed plugins on the same schedule.
const stoppedPluginCheckInterval = 30 * time.Second

// externalMCPRebuilder rebuilds the external MCP server in the background.
type externalMCPRebuilder interface {
	RequestRebuild()
}

// pluginMCPLifecycle keeps plugin MCP servers connected only while their
// source plugin is running, whatever order the server activates plugins in.
type pluginMCPLifecycle struct {
	registry  *mcp.ClientManager
	rebuilder externalMCPRebuilder // nil when the external MCP server is unavailable
	log       *pluginapi.LogService

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newPluginMCPLifecycle(registry *mcp.ClientManager, rebuilder externalMCPRebuilder, log *pluginapi.LogService) *pluginMCPLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return &pluginMCPLifecycle{
		registry:  registry,
		rebuilder: rebuilder,
		log:       log,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// reconcileKnown asks every known, non-live plugin server's source plugin to
// register again. Source plugins that started before this plugin could serve
// their registration, or that stopped retrying, come back this way.
func (l *pluginMCPLifecycle) reconcileKnown() {
	for _, known := range l.registry.KnownPluginServers() {
		l.wg.Go(func() {
			promoted, err := l.registry.ReconcileKnownPluginServer(l.ctx, known.PluginID)
			if err != nil {
				if l.ctx.Err() == nil {
					l.log.Warn("Failed to bring plugin MCP server back into service", "plugin_id", known.PluginID, "error", err.Error())
				}
				return
			}
			if promoted {
				l.log.Info("Plugin MCP server is back in service", "plugin_id", known.PluginID)
				l.requestRebuild()
			}
		})
	}
}

// watchStoppedPlugins periodically takes plugin servers out of service when
// their source plugin stopped without unregistering, for example after a crash.
func (l *pluginMCPLifecycle) watchStoppedPlugins(interval time.Duration) {
	l.wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-l.ctx.Done():
				return
			case <-ticker.C:
			}
			if dropped := l.registry.DropStoppedPluginServers(); len(dropped) > 0 {
				l.log.Info("Took plugin MCP servers of stopped plugins out of service", "plugin_ids", dropped)
				l.requestRebuild()
			}
		}
	})
}

func (l *pluginMCPLifecycle) requestRebuild() {
	if l.rebuilder != nil {
		l.rebuilder.RequestRebuild()
	}
}

// close stops the background work and waits for it to finish.
func (l *pluginMCPLifecycle) close() {
	if l == nil {
		return
	}
	l.cancel()
	l.wg.Wait()
}
