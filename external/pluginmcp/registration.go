// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pluginmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	registerPath   = "/bridge/v1/mcp/register"
	unregisterPath = "/bridge/v1/mcp/unregister"
)

// unregisterTimeout bounds Unregister()'s blocking POST so a hung Agents
// plugin cannot stall OnDeactivate. var (not const) for test override.
var unregisterTimeout = 5 * time.Second

// Register asynchronously registers this server with the Agents plugin and
// returns immediately. The background goroutine is tracked via s.regWG so
// Unregister() can wait for it to drain before posting /unregister.
func (s *Server) Register() error {
	s.regWG.Add(1)
	go func() {
		defer s.regWG.Done()
		s.registerWithBackoff(s.regCtx)
	}()
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
			log.Printf("pluginmcp: registration with Agents plugin failed permanently (plugin_id=%s): %v", s.config.PluginID, err)
			return
		}
		if attempt == s.retry.maxAttempts {
			log.Printf("pluginmcp: registration with Agents plugin gave up after %d attempts (plugin_id=%s): %v", attempt, s.config.PluginID, err)
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
	if s.pluginAPI == nil {
		return false, errors.New("pluginmcp: PluginAPI is required for registration")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return false, fmt.Errorf("marshal payload: %w", err)
	}
	url := "/" + agentsPluginID + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp := s.pluginAPI.PluginHTTP(req)
	if resp == nil {
		return true, fmt.Errorf("PluginHTTP returned nil response (Agents plugin likely not loaded)")
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("pluginmcp: closing registration response body: %v", cerr)
		}
	}()

	switch {
	case resp.StatusCode == http.StatusOK:
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return false, fmt.Errorf("drain response body: %w", err)
		}
		return false, nil
	case resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		msg, err := io.ReadAll(resp.Body)
		if err != nil {
			return true, fmt.Errorf("read error response body: %w", err)
		}
		return true, fmt.Errorf("status %d: %s", resp.StatusCode, string(msg))
	default:
		msg, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, fmt.Errorf("read error response body: %w", err)
		}
		return false, fmt.Errorf("status %d: %s", resp.StatusCode, string(msg))
	}
}

// Unregister synchronously unregisters this server with the Agents plugin.
// Cancels any pending Register() retries, waits (bounded) for an in-flight
// register attempt to drain so a late /register cannot land after our
// /unregister, then fires one POST. Intended for OnDeactivate: bounded
// wait, single attempt.
func (s *Server) Unregister() error {
	s.regCancel()

	done := make(chan struct{})
	go func() {
		s.regWG.Wait()
		close(done)
	}()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), unregisterTimeout)
	defer waitCancel()
	select {
	case <-done:
	case <-waitCtx.Done():
	}

	ctx, cancel := context.WithTimeout(context.Background(), unregisterTimeout)
	defer cancel()
	_, err := s.postRegistration(ctx, unregisterPath, s.config)
	return err
}
