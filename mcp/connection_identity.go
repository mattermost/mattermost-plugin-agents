// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// originIdentity is the set of inputs that decide whether a cached MCP session
// survives a configuration change. Two sessions for one origin are
// interchangeable exactly when their identities are equal, so the type is
// deliberately comparable and holds no tool policy or presentation fields.
//
// The zero value means "no live server for this origin", which is what a
// lookup yields for an origin the configuration no longer contains.
type originIdentity struct {
	// kind is "remote", "plugin", or "embedded".
	kind string
	// endpoint is the raw BaseURL, the plugin ID, or the embedded server's
	// address. A cosmetic URL edit (trailing slash) is a new endpoint and
	// reconnects; duplicate detection still compares canonical URLs.
	endpoint string
	name     string
	// path is the plugin server's HTTP path, empty for the other kinds.
	path    string
	enabled bool
	// usable is false for a remote server whose name or URL collides with
	// another, for a plugin row no source plugin has registered, and for an
	// embedded origin with no server behind it.
	usable bool
	// credentials digests the header and client credential material, so an
	// identity can be compared or dumped without carrying a secret.
	credentials string
}

func remoteOriginIdentity(server ServerConfig, conflicting bool) originIdentity {
	return originIdentity{
		kind:        "remote",
		endpoint:    server.BaseURL,
		name:        strings.TrimSpace(server.Name),
		enabled:     server.Enabled,
		usable:      !conflicting,
		credentials: credentialDigest(server.ClientID, server.ClientSecret, server.Headers),
	}
}

// pluginOriginIdentity describes a registered plugin server. An unregistered
// row has no live identity at all: see ClientManager.pluginIdentityLocked.
func pluginOriginIdentity(cfg PluginServerConfig) originIdentity {
	return originIdentity{
		kind:     "plugin",
		endpoint: cfg.PluginID,
		name:     strings.TrimSpace(cfg.Name),
		path:     cfg.Path,
		enabled:  cfg.Enabled,
		usable:   true,
	}
}

func embeddedOriginIdentity(server EmbeddedMCPServer, enabled bool) originIdentity {
	identity := originIdentity{kind: "embedded", enabled: enabled, usable: server != nil}
	if server != nil {
		identity.endpoint = fmt.Sprintf("%p", server)
	}
	return identity
}

// credentialDigest hashes the credential material that must force a reconnect
// when it changes. Every value is quoted before hashing so no header name or
// value can impersonate a separator and collide with a different map.
func credentialDigest(clientID, clientSecret string, headers map[string]string) string {
	if clientID == "" && clientSecret == "" && len(headers) == 0 {
		return ""
	}

	quoted := make([]string, 0, 2+2*len(headers))
	quoted = append(quoted, strconv.Quote(clientID), strconv.Quote(clientSecret))
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		quoted = append(quoted, strconv.Quote(name), strconv.Quote(headers[name]))
	}

	sum := sha256.Sum256([]byte(strings.Join(quoted, "")))
	return hex.EncodeToString(sum[:])
}

func remoteOriginIdentities(cfg Config) map[string]originIdentity {
	conflicting := make(map[int]bool, len(cfg.Servers))
	for _, conflict := range cfg.ServerConflicts() {
		conflicting[conflict.Index] = true
	}

	identities := make(map[string]originIdentity, len(cfg.Servers))
	for i, server := range cfg.Servers {
		if strings.TrimSpace(server.BaseURL) == "" {
			continue
		}
		identities[server.BaseURL] = remoteOriginIdentity(server, conflicting[i])
	}
	return identities
}
