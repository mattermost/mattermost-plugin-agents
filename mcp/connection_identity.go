// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

func marshalOriginIdentity(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func identityHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return map[string]string{}
	}
	return headers
}

// remoteOriginIdentity uses the raw BaseURL spelling. A cosmetic URL edit
// (trailing slash) is a new identity and reconnects; duplicate detection
// still uses canonical URLs. Headers are a nested object so encoding/json
// sorts keys and cannot collide on "=" or newlines.
func remoteOriginIdentity(server ServerConfig, conflicting bool) string {
	return marshalOriginIdentity(struct {
		Kind         string            `json:"k"`
		Name         string            `json:"n"`
		BaseURL      string            `json:"u"`
		Headers      map[string]string `json:"h"`
		ClientID     string            `json:"id"`
		ClientSecret string            `json:"s"`
		Enabled      bool              `json:"e"`
		Conflicting  bool              `json:"c"`
	}{
		Kind:         "remote",
		Name:         strings.TrimSpace(server.Name),
		BaseURL:      server.BaseURL,
		Headers:      identityHeaders(server.Headers),
		ClientID:     server.ClientID,
		ClientSecret: server.ClientSecret,
		Enabled:      server.Enabled,
		Conflicting:  conflicting,
	})
}

func pluginOriginIdentity(cfg PluginServerConfig, registered bool) string {
	return marshalOriginIdentity(struct {
		Kind       string `json:"k"`
		Name       string `json:"n"`
		PluginID   string `json:"p"`
		Path       string `json:"path"`
		Enabled    bool   `json:"e"`
		Registered bool   `json:"r"`
	}{
		Kind:       "plugin",
		Name:       strings.TrimSpace(cfg.Name),
		PluginID:   cfg.PluginID,
		Path:       cfg.Path,
		Enabled:    cfg.Enabled,
		Registered: registered,
	})
}

func embeddedOriginIdentity(server EmbeddedMCPServer, enabled bool) string {
	embedded := "none"
	if server != nil {
		embedded = fmt.Sprintf("%p", server)
	}
	return marshalOriginIdentity(struct {
		Kind     string `json:"k"`
		Enabled  bool   `json:"e"`
		Embedded string `json:"x"`
	}{
		Kind:     "embedded",
		Enabled:  enabled,
		Embedded: embedded,
	})
}

func remoteOriginIdentities(cfg Config) map[string]string {
	conflicting := make(map[int]bool, len(cfg.Servers))
	for _, conflict := range cfg.ServerConflicts() {
		conflicting[conflict.Index] = true
	}

	identities := make(map[string]string, len(cfg.Servers))
	for i, server := range cfg.Servers {
		if strings.TrimSpace(server.BaseURL) == "" {
			continue
		}
		identities[server.BaseURL] = remoteOriginIdentity(server, conflicting[i])
	}
	return identities
}
