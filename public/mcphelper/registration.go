// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
)

// registerPath / unregisterPath are the PluginHTTP paths for registering and
// unregistering a plugin MCP server with the Agents plugin. PluginHTTP does
// its own first-path-segment routing, so the full URL must be
// "/<AiPluginID>/bridge/v1/mcp/register" — the bare
// "/plugins/mattermost-ai/..." form is NOT correct (that's for browser-origin
// requests, not inter-plugin RPC).
const (
	registerPath   = "/bridge/v1/mcp/register"
	unregisterPath = "/bridge/v1/mcp/unregister"
)

// Register asynchronously registers this server with the Agents plugin. It
// returns nil immediately; registration happens on a background goroutine
// with exponential-backoff retries (see retryPolicy).
//
// Register is safe to call from OnActivate: it does not block on the Agents
// plugin being active, so the source plugin can start up even if Agents is
// still loading. If registration never succeeds, a warning is logged via the
// standard library's log package (the plugin API logger isn't available
// through the PluginAPI interface).
//
// Calling Register more than once is permitted but starts a fresh retry loop;
// the previous goroutine will typically have already exited. Callers who need
// to re-register after a teardown should call Unregister first.
func (s *Server) Register() error {
	go s.registerWithBackoff(s.regCtx)
	return nil
}

// registerWithBackoff is the retry loop, factored out so tests can invoke it
// synchronously with a shrunken retryPolicy.
func (s *Server) registerWithBackoff(ctx context.Context) {
	delay := s.retry.baseDelay
	for attempt := 1; attempt <= s.retry.maxAttempts; attempt++ {
		retriable, err := s.registerOnce(ctx)
		if err == nil {
			return
		}
		if !retriable {
			log.Printf("mcphelper: registration with Agents plugin failed permanently (plugin_id=%s): %v", s.config.PluginID, err)
			return
		}
		if attempt == s.retry.maxAttempts {
			log.Printf("mcphelper: registration with Agents plugin gave up after %d attempts (plugin_id=%s): %v", attempt, s.config.PluginID, err)
			return
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
		delay *= 2
		if delay > s.retry.maxDelay {
			delay = s.retry.maxDelay
		}
	}
}

// registerOnce performs a single POST attempt. Returns (retriable, err).
// retriable is true if the caller should back off and retry; false if the
// failure is permanent (4xx other than 404/429). retriable is meaningless
// when err is nil.
func (s *Server) registerOnce(ctx context.Context) (bool, error) {
	return s.postRegistration(ctx, registerPath, s.config)
}

// postRegistration is the shared request plumbing for register/unregister.
// Returns (retriable, err); see registerOnce for semantics.
func (s *Server) postRegistration(ctx context.Context, path string, body any) (bool, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return false, fmt.Errorf("marshal payload: %w", err)
	}
	url := "/" + bridgeclient.AiPluginID + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp := s.pluginAPI.PluginHTTP(req)
	if resp == nil {
		return true, fmt.Errorf("PluginHTTP returned nil response (Agents plugin likely not loaded)")
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		// Drain so the body connection can be reused; ignore any error (body
		// content is advisory only for a 200).
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, nil
	case resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		msg, _ := io.ReadAll(resp.Body)
		return true, fmt.Errorf("status %d: %s", resp.StatusCode, string(msg))
	default:
		msg, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("status %d: %s", resp.StatusCode, string(msg))
	}
}

// Unregister synchronously unregisters this server with the Agents plugin. It
// cancels any pending Register() retry goroutine first, then fires one POST
// to /bridge/v1/mcp/unregister. Unregister is called from OnDeactivate, where
// we want a bounded wait: one attempt, report the outcome.
func (s *Server) Unregister() error {
	s.regCancel()
	_, err := s.postRegistration(context.Background(), unregisterPath, s.config)
	return err
}
