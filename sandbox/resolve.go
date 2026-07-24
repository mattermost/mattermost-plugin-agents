// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

// SameOriginPluginPath is the plugin-relative path of the insecure
// same-origin sandbox route (registered on the Mattermost plugin HTTP
// handler). Bootstrap URLs are composed as
// <trimmed SiteURL>/plugins/mattermost-ai + SameOriginPluginPath so
// Mattermost subpath installs keep working.
const SameOriginPluginPath = "/mcp/apps/sandbox"

// PluginIDForRoutes is the Mattermost plugin ID used in /plugins/{id}/… URLs.
const PluginIDForRoutes = "mattermost-ai"

// Mode is the effective MCP Apps sandbox serving mode.
type Mode int

const (
	// ModeOff means apps rendering is unavailable.
	ModeOff Mode = iota
	// ModeExternal means the sandbox page is served from a configured
	// external-origin SandboxURL (standalone listener + reverse proxy).
	ModeExternal
	// ModeSameOrigin means the insecure same-origin plugin route is used.
	ModeSameOrigin
)

// Disabled reasons returned in Resolved (bootstrap contract with Phase 1c).
const (
	DisabledReasonAppsDisabled      = "apps_disabled"
	DisabledReasonNoSandboxOrigin   = "no_sandbox_origin"
	DisabledReasonInvalidSandboxURL = "invalid_sandbox_url"
)

// Resolved is the canonical effective-sandbox resolution result.
type Resolved struct {
	Mode           Mode
	PageURL        string // absolute sandbox page URL for AppRenderer
	HostOrigin     string // scheme://host[:port] for postMessage / frame-ancestors
	ListenAddr     string // bind address when ModeExternal; empty otherwise
	DisabledReason string
}

// OriginFromURL returns the browser origin (scheme://host[:port]) of raw,
// normalizing default ports (:80 for http, :443 for https) away so origin
// comparisons are stable.
func OriginFromURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("URL is empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("URL must not include userinfo")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	port := parsed.Port()
	switch {
	case port == "" || (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443"):
		return parsed.Scheme + "://" + host, nil
	default:
		return parsed.Scheme + "://" + net.JoinHostPort(host, port), nil
	}
}

// ValidateAppsConfig validates MCP Apps fields including the D1 origin
// constraint when siteURL is non-empty: sandboxURL must be a different
// browser origin than the Mattermost Site URL. Same-origin values are
// always rejected — clear Sandbox URL and enable
// AllowInsecureSameOriginSandbox to use the plugin-route fallback.
func ValidateAppsConfig(apps config.MCPAppsConfig, siteURL string) error {
	if err := apps.Validate(); err != nil {
		return err
	}

	sandboxURL := strings.TrimSpace(apps.SandboxURL)
	if sandboxURL == "" || strings.TrimSpace(siteURL) == "" {
		return nil
	}
	sandboxOrigin, err := OriginFromURL(sandboxURL)
	if err != nil {
		return fmt.Errorf("mcp apps sandboxURL: %w", err)
	}
	siteOrigin, err := OriginFromURL(siteURL)
	if err != nil {
		// Site URL shape is a Mattermost concern; skip origin equality if unparseable.
		return nil
	}
	if sandboxOrigin == siteOrigin {
		return fmt.Errorf("mcp apps sandboxURL origin must differ from the Mattermost Site URL origin; clear Sandbox URL to use the insecure same-origin fallback")
	}
	return nil
}

// Resolve computes the effective sandbox mode and URLs from apps config and
// the Mattermost Site URL. It is the single source of truth for bootstrap,
// listener reconciliation, and the same-origin route gate.
//
// Precedence: apps disabled → off; sandboxURL set on a different origin →
// external page URL; else insecure opt-in → same-origin plugin route; else
// off. A sandboxURL on the Mattermost origin always fails closed
// (ModeOff + DisabledReasonInvalidSandboxURL) — external mode means a
// genuinely different origin; clear Sandbox URL to select the fallback.
func Resolve(apps config.MCPAppsConfig, siteURL string) Resolved {
	if !apps.Enabled {
		return Resolved{Mode: ModeOff, DisabledReason: DisabledReasonAppsDisabled}
	}

	hostOrigin, hostErr := OriginFromURL(siteURL)

	if sandboxURL := strings.TrimSpace(apps.SandboxURL); sandboxURL != "" {
		tmp := config.MCPAppsConfig{SandboxURL: sandboxURL}
		if err := tmp.Validate(); err != nil {
			return Resolved{Mode: ModeOff, DisabledReason: DisabledReasonInvalidSandboxURL}
		}
		sandboxOrigin, err := OriginFromURL(sandboxURL)
		if err != nil {
			return Resolved{Mode: ModeOff, DisabledReason: DisabledReasonInvalidSandboxURL}
		}
		if hostErr == nil && sandboxOrigin == hostOrigin {
			return Resolved{Mode: ModeOff, DisabledReason: DisabledReasonInvalidSandboxURL}
		}
		if hostErr != nil {
			return Resolved{Mode: ModeOff, DisabledReason: DisabledReasonNoSandboxOrigin}
		}
		addr := strings.TrimSpace(apps.SandboxListenAddress)
		if addr == "" {
			addr = config.DefaultMCPAppsSandboxListenAddress
		}
		return Resolved{
			Mode:       ModeExternal,
			PageURL:    strings.TrimRight(sandboxURL, "/") + "/sandbox.html",
			HostOrigin: hostOrigin,
			ListenAddr: addr,
		}
	}

	if apps.AllowInsecureSameOriginSandbox {
		if hostErr != nil {
			return Resolved{Mode: ModeOff, DisabledReason: DisabledReasonNoSandboxOrigin}
		}
		base := strings.TrimRight(strings.TrimSpace(siteURL), "/")
		return Resolved{
			Mode:       ModeSameOrigin,
			PageURL:    base + "/plugins/" + PluginIDForRoutes + SameOriginPluginPath,
			HostOrigin: hostOrigin,
		}
	}

	return Resolved{Mode: ModeOff, DisabledReason: DisabledReasonNoSandboxOrigin}
}

// ListenSpecFromResolved maps a Resolved result onto the listener bind
// parameters. enabled is true only for ModeExternal.
func ListenSpecFromResolved(r Resolved) (addr, hostOrigin string, enabled bool) {
	if r.Mode != ModeExternal {
		return "", "", false
	}
	return r.ListenAddr, r.HostOrigin, true
}
