// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// userIDLength is the length of a Mattermost user ID (model.NewId).
const userIDLength = 26

// Per-user OAuth state that is keyed by server name. Refresh leases are left
// to expire on their own (seconds), and pending authorization sessions are
// rejected at callback time when the server they were started for changed.
var serverNamedOAuthKeyPrefixes = []string{
	"mcp_oauth_token_v1_",
	"mcp_oauth_needed_v1_",
}

// OAuthStateClear names a remote MCP server whose users' stored OAuth state
// must be removed. ServerID identifies the server for audit records; Name is
// the name the state is stored under.
type OAuthStateClear struct {
	ServerID string
	Name     string
}

// OAuthStateToClear returns the stored per-user OAuth state a change of remote
// MCP servers from prev to next leaves pointing at the wrong server. Grants are
// stored under the server name and sent to the server's current URL, so:
//   - a deleted server's grants are cleared;
//   - a renamed server's grants under its old name are cleared, and so is any
//     state left under its new name by an earlier server with that name;
//   - a server moved to another origin (scheme, host, port) has its grants
//     cleared; a path-only change keeps them;
//   - a new server clears state left under its name by an earlier server.
//
// Servers are matched by ID. A previous server without an ID (saved before
// IDs were minted) is matched by name, so the save that mints its ID keeps
// its users' grants. A server whose identity, name, and origin are unchanged
// keeps its users' grants.
func OAuthStateToClear(prev, next []ServerConfig) []OAuthStateClear {
	nextByID := make(map[string]int, len(next))
	nextByName := make(map[string]int, len(next))
	for i, s := range next {
		if s.ID != "" {
			nextByID[s.ID] = i
		}
		nextByName[s.Name] = i
	}

	var clears []OAuthStateClear
	seen := make(map[string]bool)
	add := func(serverID, name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		clears = append(clears, OAuthStateClear{ServerID: serverID, Name: name})
	}

	// continued holds the indexes of next servers that continue a previous one.
	continued := make(map[int]bool, len(next))
	for _, old := range prev {
		lookup, key := nextByID, old.ID
		if old.ID == "" {
			lookup, key = nextByName, old.Name
		}
		i, kept := lookup[key]
		if !kept {
			add(old.ID, old.Name)
			continue
		}
		continued[i] = true
		updated := next[i]
		switch {
		case updated.Name != old.Name:
			add(updated.ID, old.Name)
			add(updated.ID, updated.Name)
		case originChanged(old.BaseURL, updated.BaseURL):
			add(updated.ID, old.Name)
		}
	}
	for i, s := range next {
		if !continued[i] {
			add(s.ID, s.Name)
		}
	}
	return clears
}

// originChanged reports whether two server URLs point at different origins.
// URLs whose origin cannot be determined count as changed unless identical.
func originChanged(before, after string) bool {
	a := strings.ToLower(canonicalOriginFromString(before))
	b := strings.ToLower(canonicalOriginFromString(after))
	if a == "" || b == "" {
		return before != after
	}
	return a != b
}

// sameServerOrigin reports whether two server URLs share an origin.
func sameServerOrigin(a, b string) bool {
	return !originChanged(a, b)
}

// ClearServerOAuthState deletes every user's stored OAuth grant and
// sign-in-needed state for the named servers. It lists all plugin KV keys
// (the store has no server-side prefix filter) and returns how many keys it
// deleted along with any list or delete errors; deletes continue past a
// failed one so a single error does not leave other servers' state behind.
func ClearServerOAuthState(ctx context.Context, kv KVStore, names []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}

	// Keys are collected before deleting so deletions do not shift later pages.
	var keys []string
	for page := 0; ; page++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		pageKeys, err := kv.ListKeys(page, listKeysPageSize)
		if err != nil {
			return 0, fmt.Errorf("failed to list KV keys: %w", err)
		}
		for _, key := range pageKeys {
			if name, ok := serverNameFromOAuthKey(key); ok && wanted[name] {
				keys = append(keys, key)
			}
		}
		if len(pageKeys) < listKeysPageSize {
			break
		}
	}

	deleted := 0
	var errs []error
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return deleted, errors.Join(append(errs, err)...)
		}
		if err := kv.Delete(key); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete stored OAuth state: %w", err))
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}

// serverNameFromOAuthKey returns the server name a per-user OAuth key is
// stored under. Keys are prefix + 26-character user ID + "_" + server name;
// the name may itself contain underscores.
func serverNameFromOAuthKey(key string) (string, bool) {
	for _, prefix := range serverNamedOAuthKeyPrefixes {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		if len(rest) < userIDLength+1 || rest[userIDLength] != '_' || !model.IsValidId(rest[:userIDLength]) {
			return "", false
		}
		return rest[userIDLength+1:], true
	}
	return "", false
}
