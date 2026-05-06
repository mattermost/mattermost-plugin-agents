// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	BeforeHookKeyTTL       = 30 * time.Minute
	beforeHookKeyPrefix    = "beforeHook:"
	beforeHookSecretLength = 32
)

var (
	ErrBeforeHookKeyNotFound   = errors.New("before-hook key not found")
	ErrInvalidBeforeHookConfig = errors.New("invalid before-hook config")
)

// BeforeHookEntry is the trusted callback target stored for a short-lived hook key.
type BeforeHookEntry struct {
	UserID      string `json:"user_id"`
	ToolName    string `json:"tool_name"`
	CallbackURL string `json:"callback_url"`
}

// BeforeHookStore owns short-lived before-hook key persistence.
type BeforeHookStore struct {
	kv KVStore
}

// NewBeforeHookStore creates a store for short-lived before-hook keys.
func NewBeforeHookStore(kv KVStore) *BeforeHookStore {
	return &BeforeHookStore{kv: kv}
}

func buildHookCallbackURL(trustedPluginID, callbackPath string) (string, error) {
	trustedPluginID = strings.TrimSpace(trustedPluginID)
	if trustedPluginID == "" {
		return "", fmt.Errorf("missing hook plugin id")
	}
	if !strings.HasPrefix(callbackPath, "/") {
		return "", fmt.Errorf("callback path must start with /")
	}

	scope := path.Join("/plugins", trustedPluginID)
	joined := path.Join(scope, callbackPath)
	if joined != scope && !strings.HasPrefix(joined, scope+"/") {
		return "", fmt.Errorf("callback path escapes plugin namespace")
	}

	return joined, nil
}

func buildBeforeHookKey() string {
	return beforeHookKeyPrefix + model.NewRandomString(beforeHookSecretLength)
}

// Issue stores a trusted callback endpoint and returns an opaque key bound to the user and tool.
func (s *BeforeHookStore) Issue(userID, toolName, pluginID, beforeCallback string) (string, error) {
	if s == nil || s.kv == nil {
		return "", errors.New("before-hook store not configured")
	}
	userID = strings.TrimSpace(userID)
	toolName = strings.TrimSpace(toolName)
	if userID == "" {
		return "", errors.New("tool_hooks requires user_id")
	}
	if toolName == "" {
		return "", errors.New("tool_hooks requires tool name")
	}

	trustedPluginID := strings.TrimSpace(pluginID)
	callbackURL, err := buildHookCallbackURL(trustedPluginID, beforeCallback)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidBeforeHookConfig, err)
	}

	key := buildBeforeHookKey()
	saved, err := s.kv.Set(key, BeforeHookEntry{
		UserID:      userID,
		ToolName:    toolName,
		CallbackURL: callbackURL,
	}, pluginapi.SetExpiry(BeforeHookKeyTTL))
	if err != nil {
		return "", fmt.Errorf("store before-hook key: %w", err)
	}
	if !saved {
		return "", errors.New("store before-hook key: not saved")
	}

	return key, nil
}

// Resolve returns a trusted callback URL for a key bound to userID and toolName.
// The key remains valid until its KV TTL expires so a single bridge run can call
// the same tool more than once.
func (s *BeforeHookStore) Resolve(userID, toolName, hookKey string) (BeforeHookEntry, error) {
	if s == nil || s.kv == nil {
		return BeforeHookEntry{}, errors.New("before-hook store not configured")
	}
	userID = strings.TrimSpace(userID)
	toolName = strings.TrimSpace(toolName)
	if userID == "" || toolName == "" || hookKey == "" || !strings.HasPrefix(hookKey, beforeHookKeyPrefix) {
		return BeforeHookEntry{}, ErrBeforeHookKeyNotFound
	}

	var entry BeforeHookEntry
	if err := s.kv.Get(hookKey, &entry); err != nil {
		return BeforeHookEntry{}, fmt.Errorf("get before-hook key: %w", err)
	}
	if entry.UserID == "" || entry.ToolName == "" || entry.CallbackURL == "" || entry.UserID != userID || entry.ToolName != toolName {
		return BeforeHookEntry{}, ErrBeforeHookKeyNotFound
	}

	return entry, nil
}

// Delete removes a before-hook key. It is used for best-effort cleanup once a
// bridge request completes; TTL remains the fallback for interrupted requests.
func (s *BeforeHookStore) Delete(hookKey string) error {
	if s == nil || s.kv == nil {
		return errors.New("before-hook store not configured")
	}
	if hookKey == "" {
		return nil
	}
	return s.kv.Delete(hookKey)
}
