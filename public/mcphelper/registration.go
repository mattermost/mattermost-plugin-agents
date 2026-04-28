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

const (
	registerPath   = "/bridge/v1/mcp/register"
	unregisterPath = "/bridge/v1/mcp/unregister"
)

// Register asynchronously registers this server with the Agents plugin and
// returns immediately.
func (s *Server) Register() error {
	go s.registerWithBackoff(s.regCtx)
	return nil
}

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

// registerOnce performs a single POST attempt. retriable is meaningless
// when err is nil; 404/429/5xx are retriable, other 4xx are permanent.
func (s *Server) registerOnce(ctx context.Context) (bool, error) {
	return s.postRegistration(ctx, registerPath, s.config)
}

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

// Unregister synchronously unregisters this server with the Agents plugin.
// Cancels any pending Register() retries, then fires one POST. Intended for
// OnDeactivate: bounded wait, single attempt.
func (s *Server) Unregister() error {
	s.regCancel()
	_, err := s.postRegistration(context.Background(), unregisterPath, s.config)
	return err
}
