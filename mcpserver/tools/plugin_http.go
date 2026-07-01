// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
)

// postPluginJSON marshals reqBody to JSON and POSTs it to a plugin callback
// endpoint, forwarding the auth token and user id from ctx (falling back to
// fallbackUserID when ctx carries no user id). It returns the response status
// code and raw body; callers decode the body and map status codes themselves.
// This centralizes the request plumbing shared by the HTTP-backed capability
// services (semantic search, file content) so a new one is a thin wrapper.
func postPluginJSON(ctx context.Context, client *http.Client, url string, reqBody any, fallbackUserID string) (int, []byte, error) {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token, ok := ctx.Value(auth.AuthTokenContextKey).(string); ok && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctxUserID, ok := ctx.Value(auth.UserIDContextKey).(string); ok && ctxUserID != "" {
		req.Header.Set("Mattermost-User-Id", ctxUserID)
	} else if fallbackUserID != "" {
		req.Header.Set("Mattermost-User-Id", fallbackUserID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}
