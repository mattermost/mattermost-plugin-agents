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
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
)

const hookHTTPTimeout = 30 * time.Second

var hookHTTPClient = &http.Client{
	Timeout: hookHTTPTimeout,
}

// buildHookURL constructs the URL the agents plugin will POST to for a tool hook.
//
// The callback path is supplied by another plugin via the bridge, so it must be
// confined to that plugin's own /plugins/<pluginID>/ namespace. JoinPath runs
// path.Clean for us so "./", duplicate slashes, and any "../" sequences are
// collapsed; the scope check then rejects anything whose cleaned path leaves
// the plugin's URL space (e.g. "/../../api/v4/users/me" cleans to
// "/api/v4/users/me", which fails the prefix test).
//
// This prevents a malicious or buggy caller from having the agents plugin
// replay the user's auth token against other plugins' routes or core APIs.
func buildHookURL(baseURL, pluginID, callbackPath string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("missing Mattermost base URL for tool hooks")
	}
	if strings.TrimSpace(pluginID) == "" {
		return "", fmt.Errorf("missing hook plugin id")
	}
	if !strings.HasPrefix(callbackPath, "/") {
		return "", fmt.Errorf("callback path must start with /")
	}

	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	if parsedBase.Scheme == "" || parsedBase.Host == "" {
		return "", fmt.Errorf("base URL must include scheme and host")
	}

	scope := parsedBase.JoinPath("plugins", pluginID)
	joined := scope.JoinPath(callbackPath)
	if joined.Path != scope.Path && !strings.HasPrefix(joined.Path, scope.Path+"/") {
		return "", fmt.Errorf("callback path escapes plugin namespace")
	}

	return joined.String(), nil
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

	reqBody := mcptool.BeforeHookRequest{
		ToolName: toolName,
		Args:     argsJSON,
		UserID:   mcpCtx.UserID,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("tool %s: before-hook failed: marshal request: %w", toolName, err)
	}

	authToken := ""
	if mcpCtx.Client != nil {
		authToken = mcpCtx.Client.AuthToken
	}

	respBody, err := postHookJSON(mcpCtx.Ctx, url, authToken, payload)
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
