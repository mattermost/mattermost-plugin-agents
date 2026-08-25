// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

// ErrDuplicateMCPServer reports a remote MCP server list that reuses a name or
// points two entries at the same endpoint.
var ErrDuplicateMCPServer = errors.New("duplicate MCP server")

// MCPServerConflictReason classifies why a remote MCP server entry conflicts
// with another entry in the same configuration.
type MCPServerConflictReason string

const (
	// MCPServerConflictDuplicateName means two entries share a server name.
	// Names identify servers in the per-user client map, the shared tools
	// cache, and stored OAuth grants, so they must be unique.
	MCPServerConflictDuplicateName MCPServerConflictReason = "duplicate_name"
	// MCPServerConflictDuplicateURL means two entries resolve to the same
	// canonical endpoint URL.
	MCPServerConflictDuplicateURL MCPServerConflictReason = "duplicate_url"
)

// MCPServerConflict describes one conflicting remote MCP server entry.
type MCPServerConflict struct {
	// Index is the entry's position in MCPConfig.Servers.
	Index   int
	Name    string
	BaseURL string
	Reason  MCPServerConflictReason
}

func (c MCPServerConflict) Error() string {
	switch c.Reason {
	case MCPServerConflictDuplicateName:
		return fmt.Sprintf("%v: name %q is used by more than one server", ErrDuplicateMCPServer, c.Name)
	case MCPServerConflictDuplicateURL:
		// Named by its canonical form: that is what the entries collide on, and
		// it is the same text for every member of the group.
		return fmt.Sprintf("%v: endpoint %q is configured on more than one server", ErrDuplicateMCPServer, CanonicalMCPEndpointURL(c.BaseURL))
	default:
		return fmt.Sprintf("%v: server %q", ErrDuplicateMCPServer, c.Name)
	}
}

func (c MCPServerConflict) Unwrap() error {
	return ErrDuplicateMCPServer
}

// conflictKey identifies the colliding group, so a collision can be reported
// once instead of once per member.
func (c MCPServerConflict) conflictKey() string {
	if c.Reason == MCPServerConflictDuplicateURL {
		return string(c.Reason) + "\x00" + CanonicalMCPEndpointURL(c.BaseURL)
	}
	return string(c.Reason) + "\x00" + strings.TrimSpace(c.Name)
}

// CanonicalMCPEndpointURL returns a comparable form of an MCP endpoint URL:
// scheme and host lowercased, a default port dropped, and one otherwise
// equivalent trailing slash removed. Path and query differences are preserved
// because they select different endpoints on the same host. Input that does not
// parse as an absolute URL is compared case-insensitively as-is.
func CanonicalMCPEndpointURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.ToLower(trimmed)
	}

	scheme := strings.ToLower(parsed.Scheme)

	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if isDefaultPortForScheme(scheme, port) {
		port = ""
	}
	switch {
	case port != "":
		host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		// Bare IPv6 literal; keep it bracketed so the result stays parseable.
		host = "[" + host + "]"
	}

	var canonical strings.Builder
	canonical.WriteString(scheme)
	canonical.WriteString("://")
	if userinfo := parsed.User.String(); userinfo != "" {
		canonical.WriteString(userinfo)
		canonical.WriteByte('@')
	}
	canonical.WriteString(host)
	canonical.WriteString(strings.TrimSuffix(parsed.EscapedPath(), "/"))
	if query := parsed.Query().Encode(); query != "" {
		canonical.WriteByte('?')
		canonical.WriteString(query)
	}

	return canonical.String()
}

func isDefaultPortForScheme(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

// ServerConflicts returns every remote MCP server entry that collides with
// another entry, by name or by canonical endpoint URL. All members of a
// colliding group are reported: with nothing to distinguish them, none can be
// safely picked over the others. Entries with a blank name or URL are skipped;
// they are already unusable for other reasons.
func (c MCPConfig) ServerConflicts() []MCPServerConflict {
	byName := make(map[string][]int, len(c.Servers))
	byURL := make(map[string][]int, len(c.Servers))

	for i := range c.Servers {
		name := strings.TrimSpace(c.Servers[i].Name)
		if name != "" {
			byName[name] = append(byName[name], i)
		}
		if canonical := CanonicalMCPEndpointURL(c.Servers[i].BaseURL); canonical != "" {
			byURL[canonical] = append(byURL[canonical], i)
		}
	}

	reasons := make(map[int]MCPServerConflictReason, len(c.Servers))
	// Name conflicts are recorded second so they win the reason when an entry
	// duplicates both, which is the more actionable of the two messages.
	for _, indexes := range byURL {
		markConflicts(reasons, indexes, MCPServerConflictDuplicateURL)
	}
	for _, indexes := range byName {
		markConflicts(reasons, indexes, MCPServerConflictDuplicateName)
	}

	if len(reasons) == 0 {
		return nil
	}

	conflicts := make([]MCPServerConflict, 0, len(reasons))
	for index, reason := range reasons {
		conflicts = append(conflicts, MCPServerConflict{
			Index:   index,
			Name:    c.Servers[index].Name,
			BaseURL: c.Servers[index].BaseURL,
			Reason:  reason,
		})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].Index < conflicts[j].Index
	})

	return conflicts
}

func markConflicts(reasons map[int]MCPServerConflictReason, indexes []int, reason MCPServerConflictReason) {
	if len(indexes) < 2 {
		return
	}
	for _, index := range indexes {
		reasons[index] = reason
	}
}

// Validate reports duplicate remote MCP server names and endpoint URLs. It is
// used to reject a new or updated configuration before it is persisted;
// already-stored duplicates are tolerated at runtime (see ServerConflicts) so
// a bad stored config cannot block plugin activation.
func (c MCPConfig) Validate() error {
	conflicts := c.ServerConflicts()
	if len(conflicts) == 0 {
		return nil
	}

	// One error per colliding value rather than per colliding entry: repeating
	// the same collision for every member of a group is just noise.
	seen := make(map[string]bool, len(conflicts))
	var err error
	for _, conflict := range conflicts {
		key := conflict.conflictKey()
		if seen[key] {
			continue
		}
		seen[key] = true
		err = errors.Join(err, conflict)
	}
	return err
}
