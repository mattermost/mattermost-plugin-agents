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
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
)

const hookHTTPTimeout = 30 * time.Second

var hookHTTPClient = &http.Client{
	Timeout: hookHTTPTimeout,
}

func buildHookURL(baseURL, pluginID, callbackPath string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("missing Mattermost base URL for tool hooks")
	}
	if strings.TrimSpace(pluginID) == "" {
		return "", fmt.Errorf("missing hook plugin id")
	}
	if callbackPath == "" {
		return "", fmt.Errorf("empty callback path")
	}
	if !strings.HasPrefix(callbackPath, "/") {
		return "", fmt.Errorf("callback path must start with /")
	}
	base := strings.TrimRight(baseURL, "/")
	return fmt.Sprintf("%s/plugins/%s%s", base, pluginID, callbackPath), nil
}

func postHookJSON(ctx context.Context, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token, ok := ctx.Value(auth.AuthTokenContextKey).(string); ok && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := hookHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return respBody, nil
}

// RunBeforeHook is a no-op when no before-hook is registered for toolName.
// Otherwise it POSTs to the calling plugin and returns an error if the hook rejects or fails (fail-closed).
// args is the validated, decoded resolver argument struct; the hook receives its JSON form.
func RunBeforeHook(mcpCtx *MCPToolContext, toolName string, args any) error {
	if mcpCtx == nil || mcpCtx.ToolHooks == nil {
		return nil
	}
	cfg, ok := mcpCtx.ToolHooks[toolName]
	if !ok || cfg.BeforeCallback == "" {
		return nil
	}

	url, err := buildHookURL(mcpCtx.MMServerURL, mcpCtx.HookPluginID, cfg.BeforeCallback)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: %w", toolName, err)
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: marshal args: %w", toolName, err)
	}

	userID, _ := mcpCtx.Ctx.Value(auth.UserIDContextKey).(string)
	reqBody := mcptool.BeforeHookRequest{
		ToolName: toolName,
		Args:     argsJSON,
		UserID:   userID,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: marshal request: %w", toolName, err)
	}

	respBody, err := postHookJSON(mcpCtx.Ctx, url, payload)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: %w", toolName, err)
	}

	var hookResp mcptool.BeforeHookResponse
	if err := json.Unmarshal(respBody, &hookResp); err != nil {
		return fmt.Errorf("tool %s: before-hook failed: invalid response", toolName)
	}
	if msg := strings.TrimSpace(hookResp.Error); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return nil
}
