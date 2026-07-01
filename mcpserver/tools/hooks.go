// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/public/mcptool"
)

const hookHTTPTimeout = 30 * time.Second

var hookHTTPClient = &http.Client{
	Timeout: hookHTTPTimeout,
}

// buildHookURL constructs the absolute URL for a trusted root-relative callback
// URL returned by the agents plugin. The URL must stay under /plugins/ after
// path cleaning so a malformed KV value cannot redirect the user's token.
func buildHookURL(baseURL, callbackURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("missing Mattermost base URL for tool hooks")
	}
	parsedCallbackURL, err := url.ParseRequestURI(callbackURL)
	if err != nil {
		parsedURL, parseErr := url.Parse(callbackURL)
		if parseErr == nil && !parsedURL.IsAbs() && parsedURL.Host == "" {
			return "", fmt.Errorf("callback URL must start with /plugins/")
		}
		return "", fmt.Errorf("invalid callback URL")
	}
	if parsedCallbackURL.IsAbs() || parsedCallbackURL.Host != "" || parsedCallbackURL.RawQuery != "" {
		return "", fmt.Errorf("invalid callback URL")
	}
	callbackPath := parsedCallbackURL.Path
	if !strings.HasPrefix(callbackPath, "/plugins/") {
		return "", fmt.Errorf("callback URL must start with /plugins/")
	}
	pluginPath := strings.TrimPrefix(callbackPath, "/plugins/")
	pluginID, _, ok := strings.Cut(pluginPath, "/")
	if !ok || pluginID == "" {
		return "", fmt.Errorf("invalid callback URL")
	}

	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	if parsedBase.Scheme == "" || parsedBase.Host == "" {
		return "", fmt.Errorf("base URL must include scheme and host")
	}

	cleanedCallbackPath := path.Clean(callbackPath)
	scopePath := path.Join("/plugins", pluginID)
	if cleanedCallbackPath != scopePath && !strings.HasPrefix(cleanedCallbackPath, scopePath+"/") {
		return "", fmt.Errorf("invalid callback URL")
	}

	return parsedBase.JoinPath(cleanedCallbackPath).String(), nil
}

func resolveBeforeHookEndpoint(baseURL, userID, toolName, beforeHookKey string, resolver func(userID, toolName, hookKey string) (string, error)) (string, error) {
	if strings.TrimSpace(toolName) == "" {
		return "", fmt.Errorf("missing tool name")
	}
	if strings.TrimSpace(beforeHookKey) == "" {
		return "", fmt.Errorf("missing before-hook key")
	}
	if resolver == nil {
		return "", fmt.Errorf("missing before-hook resolver")
	}
	callbackURL, err := resolver(userID, toolName, beforeHookKey)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(callbackURL) == "" {
		return "", fmt.Errorf("empty callback URL")
	}

	return buildHookURL(baseURL, callbackURL)
}

func postHookJSON(ctx context.Context, url, authToken string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
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
	if !ok || cfg.BeforeHookKey == "" {
		return nil
	}

	authToken := ""
	if mcpCtx.Client != nil {
		authToken = mcpCtx.Client.AuthToken
	}

	hookURL, err := resolveBeforeHookEndpoint(mcpCtx.MMServerURL, mcpCtx.UserID, toolName, cfg.BeforeHookKey, mcpCtx.BeforeHookResolver)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: %w", toolName, err)
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: marshal args: %w", toolName, err)
	}

	reqBody := mcptool.BeforeHookRequest{
		ToolName: toolName,
		Args:     argsJSON,
		UserID:   mcpCtx.UserID,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: marshal request: %w", toolName, err)
	}

	respBody, err := postHookJSON(mcpCtx.Ctx, hookURL, authToken, payload)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: %w", toolName, err)
	}

	var hookResp mcptool.BeforeHookResponse
	if err := json.Unmarshal(respBody, &hookResp); err != nil {
		return fmt.Errorf("tool %s: before-hook failed: invalid response", toolName)
	}
	if msg := strings.TrimSpace(hookResp.Error); msg != "" {
		return errors.New(msg)
	}
	return nil
}
