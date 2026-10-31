// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

// pluginMCPRegistry is the part of mcp.ClientManager that keeps plugin MCP
// servers in step with their source plugins' lifecycle.
type pluginMCPRegistry interface {
	GetPluginServer(pluginID string) (mcp.PluginServerConfig, bool)
	IsPluginRegistered(pluginID string) bool
	UnregisterPluginServer(pluginID string)
	KnownPluginServers() []mcp.PluginServerConfig
	ReconcileKnownPluginServer(ctx context.Context, pluginID string) (bool, error)
}

// externalMCPRebuilder rebuilds the external MCP server in the background.
type externalMCPRebuilder interface {
	RequestRebuild()
}

type lifecycleLogger interface {
	Info(message string, keyValuePairs ...any)
	Warn(message string, keyValuePairs ...any)
}

// pluginMCPLifecycle reacts to source plugins starting and stopping, and to
// this plugin starting, so plugin MCP servers are connected only while their
// source plugin is running, whatever order the server activates plugins in.
type pluginMCPLifecycle struct {
	registry  pluginMCPRegistry
	rebuilder externalMCPRebuilder // nil when the external MCP server is unavailable
	log       lifecycleLogger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newPluginMCPLifecycle(registry pluginMCPRegistry, rebuilder externalMCPRebuilder, log lifecycleLogger) *pluginMCPLifecycle {
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
		l.goReconcile(known.PluginID)
	}
}

// pluginActivated handles OnPluginActivated for any plugin on this server.
func (l *pluginMCPLifecycle) pluginActivated(pluginID string) {
	if l.registry.IsPluginRegistered(pluginID) {
		// It registered while still in OnActivate, so discovery was skipped
		// until now.
		l.requestRebuild()
		return
	}
	if _, known := l.registry.GetPluginServer(pluginID); known {
		l.goReconcile(pluginID)
	}
}

// pluginDeactivated handles OnPluginDeactivated for any plugin on this
// server. A plugin that crashed or was disabled cannot unregister itself.
func (l *pluginMCPLifecycle) pluginDeactivated(pluginID string) {
	if !l.registry.IsPluginRegistered(pluginID) {
		return
	}
	l.registry.UnregisterPluginServer(pluginID)
	l.requestRebuild()
}

func (l *pluginMCPLifecycle) goReconcile(pluginID string) {
	l.wg.Go(func() {
		promoted, err := l.registry.ReconcileKnownPluginServer(l.ctx, pluginID)
		if err != nil {
			if l.ctx.Err() == nil {
				l.log.Warn("Failed to bring plugin MCP server back into service", "plugin_id", pluginID, "error", err.Error())
			}
			return
		}
		if promoted {
			l.log.Info("Plugin MCP server is back in service", "plugin_id", pluginID)
			l.requestRebuild()
		}
	})
}

func (l *pluginMCPLifecycle) requestRebuild() {
	if l.rebuilder != nil {
		l.rebuilder.RequestRebuild()
	}
}

// close cancels in-flight reconciliation and waits for it to finish.
func (l *pluginMCPLifecycle) close() {
	if l == nil {
		return
	}
	l.cancel()
	l.wg.Wait()
}

// OnPluginActivated is invoked when another plugin on this server finishes
// activating and can serve requests.
func (p *Plugin) OnPluginActivated(manifest *model.Manifest) {
	if p.mcpLifecycle == nil || manifest == nil {
		return
	}
	p.mcpLifecycle.pluginActivated(manifest.Id)
}

// OnPluginDeactivated is invoked when another plugin on this server is
// deactivated.
func (p *Plugin) OnPluginDeactivated(manifest *model.Manifest) {
	if p.mcpLifecycle == nil || manifest == nil {
		return
	}
	p.mcpLifecycle.pluginDeactivated(manifest.Id)
}
