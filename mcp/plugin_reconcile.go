// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/mattermost/mattermost/server/public/model"
)

// pluginReconcileTimeout bounds the re-register request and the probe for one
// source plugin.
const pluginReconcileTimeout = 10 * time.Second

// ReconcileKnownPluginServer brings a known, non-live plugin server back into
// service when its source plugin is running. It asks the source plugin to
// register again; the registration then arrives through the bridge register
// route. A source plugin built before it could be asked does not acknowledge
// the request, so its MCP endpoint is probed instead and, when it answers, the
// known entry is promoted directly. If the probe is rejected with 401 or 403
// (for example, an endpoint that requires a user context), the server stays
// unpromoted and waits for the plugin's own registration. It reports whether
// the entry was promoted.
//
// Nothing is contacted when pluginID is not a known, non-live entry, when
// remote and plugin MCP servers are not licensed, or when the source plugin
// is not running: it registers itself when it starts.
func (m *ClientManager) ReconcileKnownPluginServer(ctx context.Context, pluginID string) (bool, error) {
	cfg, known := m.GetPluginServer(pluginID)
	if !known || m.IsPluginRegistered(pluginID) || !m.remoteMCPAllowed() {
		return false, nil
	}
	if m.sourcePluginAPI == nil {
		return false, errors.New("no plugin API to reach source plugins")
	}
	status, err := m.sourcePluginAPI.GetPluginStatus(pluginID)
	if err != nil {
		return false, fmt.Errorf("get plugin status: %w", err)
	}
	if status == nil || status.State != model.PluginStateRunning {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(ctx, pluginReconcileTimeout)
	defer cancel()
	baseTransport := NewPluginHTTPRoundTripper(pluginID, cfg.Path, m.sourcePluginAPI)
	httpClient := PluginServerHTTPClient(baseTransport, "")

	acknowledged, err := requestPluginReregister(ctx, httpClient, cfg.Path)
	if err != nil {
		return false, fmt.Errorf("ask plugin to register again: %w", err)
	}
	if acknowledged {
		return false, nil
	}

	probeTransport := &probeStatusRoundTripper{base: baseTransport}
	session, err := ConnectPluginServer(ctx, "mattermost-agents-probe", cfg.Path, PluginServerHTTPClient(probeTransport, ""))
	if err != nil {
		if ctx.Err() == nil {
			if status := int(probeTransport.lastPostStatus.Load()); status == http.StatusUnauthorized || status == http.StatusForbidden {
				m.log.Debug("Plugin MCP server probe requires user authentication; waiting for plugin registration",
					"plugin_id", pluginID,
					"status", status,
				)
				return false, nil
			}
		}
		return false, fmt.Errorf("probe plugin MCP server: %w", err)
	}
	if closeErr := session.Close(); closeErr != nil {
		m.log.Debug("Failed to close plugin MCP probe session", "plugin_id", pluginID, "error", closeErr.Error())
	}
	return m.PromoteKnownPluginServer(pluginID), nil
}

// probeStatusRoundTripper records the HTTP status of the most recent POST
// response during a startup probe so auth rejections (401/403) can be
// distinguished from server or protocol failures without parsing SDK error
// strings.
type probeStatusRoundTripper struct {
	base           http.RoundTripper
	lastPostStatus atomic.Int64
}

func (t *probeStatusRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost {
		t.lastPostStatus.Store(0)
	}
	resp, err := t.base.RoundTrip(req)
	if req.Method == http.MethodPost && resp != nil {
		t.lastPostStatus.Store(int64(resp.StatusCode))
	}
	return resp, err
}

// requestPluginReregister sends the re-register request to a source plugin's
// MCP endpoint and reports whether the pluginmcp library acknowledged it,
// whatever the outcome.
func requestPluginReregister(ctx context.Context, httpClient *http.Client, path string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin"+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set(pluginmcp.ReregisterHeader, "1")
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.Header.Get(pluginmcp.ReregisterHeader) != "", nil
}
