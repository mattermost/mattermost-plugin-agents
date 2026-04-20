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
func RunBeforeHook(mcpCtx *MCPToolContext, toolName string, args map[string]any) error {
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

	userID, _ := mcpCtx.Ctx.Value(auth.UserIDContextKey).(string)
	reqBody := mcptool.BeforeHookRequest{
		ToolName: toolName,
		Args:     args,
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

// RunAfterHook is a no-op when no after-hook is registered for toolName.
// Otherwise it POSTs the tool output to the calling plugin and returns the unmarshaled response (fail-closed).
func RunAfterHook[T any](mcpCtx *MCPToolContext, toolName string, output T) (T, error) {
	var zero T
	if mcpCtx == nil || mcpCtx.ToolHooks == nil {
		return output, nil
	}
	cfg, ok := mcpCtx.ToolHooks[toolName]
	if !ok || cfg.AfterCallback == "" {
		return output, nil
	}

	url, err := buildHookURL(mcpCtx.MMServerURL, mcpCtx.HookPluginID, cfg.AfterCallback)
	if err != nil {
		return zero, fmt.Errorf("tool %s: after-hook failed: %w", toolName, err)
	}

	outputBytes, err := json.Marshal(output)
	if err != nil {
		return zero, fmt.Errorf("tool %s: after-hook failed: marshal output: %w", toolName, err)
	}
	userID, _ := mcpCtx.Ctx.Value(auth.UserIDContextKey).(string)
	reqBody := mcptool.AfterHookRequest{
		ToolName: toolName,
		UserID:   userID,
		Output:   outputBytes,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return zero, fmt.Errorf("tool %s: after-hook failed: marshal request: %w", toolName, err)
	}

	respBody, err := postHookJSON(mcpCtx.Ctx, url, payload)
	if err != nil {
		return zero, fmt.Errorf("tool %s: after-hook failed: %w", toolName, err)
	}

	var hookResp mcptool.AfterHookResponse
	if err := json.Unmarshal(respBody, &hookResp); err != nil {
		return zero, fmt.Errorf("tool %s: after-hook failed: invalid response envelope", toolName)
	}
	if strings.TrimSpace(hookResp.Error) != "" {
		return zero, fmt.Errorf("tool %s: aborted by policy: %s", toolName, hookResp.Error)
	}
	if len(hookResp.Output) == 0 {
		return zero, fmt.Errorf("tool %s: after-hook failed: empty output in response", toolName)
	}

	var out T
	if err := json.Unmarshal(hookResp.Output, &out); err != nil {
		return zero, fmt.Errorf("tool %s: after-hook failed: output does not match expected shape", toolName)
	}
	return out, nil
}

// RunAfterHookError is a no-op when no after-hook is registered for toolName.
// Otherwise it POSTs the resolver error to the calling plugin's after-hook URL so the plugin
// can replace or redact the message before it is returned to the LLM (fail-closed on hook failure).
// If the hook response Error field is empty, origErr is returned unchanged.
func RunAfterHookError(mcpCtx *MCPToolContext, toolName string, origErr error) error {
	if origErr == nil {
		return nil
	}
	if mcpCtx == nil || mcpCtx.ToolHooks == nil {
		return origErr
	}
	cfg, ok := mcpCtx.ToolHooks[toolName]
	if !ok || cfg.AfterCallback == "" {
		return origErr
	}

	url, err := buildHookURL(mcpCtx.MMServerURL, mcpCtx.HookPluginID, cfg.AfterCallback)
	if err != nil {
		return fmt.Errorf("tool %s: after-hook failed: %w", toolName, err)
	}

	userID, _ := mcpCtx.Ctx.Value(auth.UserIDContextKey).(string)
	reqBody := mcptool.AfterHookRequest{
		ToolName: toolName,
		UserID:   userID,
		Error:    origErr.Error(),
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("tool %s: after-hook failed: marshal request: %w", toolName, err)
	}

	respBody, err := postHookJSON(mcpCtx.Ctx, url, payload)
	if err != nil {
		return fmt.Errorf("tool %s: after-hook failed: %w", toolName, err)
	}

	var hookResp mcptool.AfterHookResponse
	if err := json.Unmarshal(respBody, &hookResp); err != nil {
		return fmt.Errorf("tool %s: after-hook failed: invalid response envelope", toolName)
	}
	if msg := strings.TrimSpace(hookResp.Error); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return origErr
}
