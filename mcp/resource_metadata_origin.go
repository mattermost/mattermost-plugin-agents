// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// ValidateResourceMetadataMatchesServerBaseURL ensures resource_metadata is on the same
// origin as the admin-configured MCP server BaseURL (scheme + host + port).
// metadataURL must be non-empty; callers should skip when empty.
func ValidateResourceMetadataMatchesServerBaseURL(serverBaseURL, metadataURL string) error {
	if metadataURL == "" {
		return fmt.Errorf("resource_metadata URL is empty")
	}

	base, err := url.Parse(serverBaseURL)
	if err != nil {
		return fmt.Errorf("invalid MCP server base URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return fmt.Errorf("MCP server base URL missing scheme or host")
	}

	meta, err := url.Parse(metadataURL)
	if err != nil {
		return fmt.Errorf("invalid resource_metadata URL: %w", err)
	}
	if meta.Scheme == "" || meta.Host == "" {
		return fmt.Errorf("resource_metadata URL missing scheme or host")
	}
	if meta.User != nil {
		return fmt.Errorf("resource_metadata URL must not contain user info")
	}

	if originComparableKey(base) != originComparableKey(meta) {
		return fmt.Errorf("resource_metadata origin does not match MCP server base URL origin")
	}
	return nil
}

// originComparableKey returns a normalized scheme+authority for same-origin comparison.
// Default HTTP/HTTPS ports are stripped; hostnames are lowercased; IPv6 is handled like mcpserver.normalizeURL.
func originComparableKey(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	hostField := u.Host

	host, port, err := net.SplitHostPort(hostField)
	if err != nil {
		return scheme + "://" + strings.ToLower(hostField)
	}

	isDefaultPort := (scheme == "http" && port == "80") ||
		(scheme == "https" && port == "443")

	if isDefaultPort {
		if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
			return scheme + "://[" + strings.ToLower(host) + "]"
		}
		return scheme + "://" + strings.ToLower(host)
	}

	return scheme + "://" + strings.ToLower(net.JoinHostPort(host, port))
}
