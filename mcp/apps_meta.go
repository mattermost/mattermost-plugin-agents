// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"unicode"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// UIExtensionID is the MCP Apps extension identifier used in
	// initialize capability negotiation (SEP-1865, stable 2026-01-26).
	UIExtensionID = "io.modelcontextprotocol/ui"

	// UIResourceMIMEType is the exact MIME type required for MCP App
	// HTML resources.
	UIResourceMIMEType = "text/html;profile=mcp-app"

	// legacyToolUIResourceURIKey is the deprecated flat _meta key still
	// emitted by real servers alongside the nested form. Remove after the
	// extension GAs and servers drop it.
	legacyToolUIResourceURIKey = "ui/resourceUri"
)

// uiClientCapabilities returns the ClientCapabilities every plugin MCP client
// declares: the MCP Apps ui extension (mimeTypes is REQUIRED by the spec)
// plus the roots capability the SDK previously declared by default. Setting
// ClientOptions.Capabilities overrides that default, so RootsV2 must be
// restated explicitly to keep the wire behavior unchanged.
func uiClientCapabilities() *mcp.ClientCapabilities {
	caps := &mcp.ClientCapabilities{
		RootsV2: &mcp.RootCapabilities{ListChanged: true},
	}
	caps.AddExtension(UIExtensionID, map[string]any{
		"mimeTypes": []any{UIResourceMIMEType},
	})
	return caps
}

// uiClientOptions returns the ClientOptions used at every mcp.NewClient call site.
func uiClientOptions() *mcp.ClientOptions {
	return &mcp.ClientOptions{Capabilities: uiClientCapabilities()}
}

// parseToolUIMeta extracts MCP Apps tool metadata from a tool's _meta map.
// The canonical nested form (_meta.ui.resourceUri / _meta.ui.visibility) is
// preferred; the deprecated flat _meta["ui/resourceUri"] is accepted as a
// fallback for ResourceURI when the nested form lacks a valid resourceUri
// (spec deprecates the flat key "before GA"). Nested visibility is kept when
// present even if the URI comes from the flat key. Returns nil when the tool
// declares no UI.
func parseToolUIMeta(meta map[string]any) *llm.ToolUIMeta {
	if len(meta) == 0 {
		return nil
	}

	out := &llm.ToolUIMeta{}
	if ui, ok := meta["ui"].(map[string]any); ok {
		if s, ok := ui["resourceUri"].(string); ok {
			out.ResourceURI = s
		}
		if vis, ok := ui["visibility"].([]any); ok {
			for _, v := range vis {
				if s, ok := v.(string); ok {
					out.Visibility = append(out.Visibility, s)
				}
			}
		}
	}

	if out.ResourceURI == "" {
		if s, ok := meta[legacyToolUIResourceURIKey].(string); ok && s != "" {
			out.ResourceURI = s
		}
	}

	if out.ResourceURI == "" && len(out.Visibility) == 0 {
		return nil
	}
	return out
}

// validateUIResourceURI reports whether uri is a well-formed ui:// URI without
// control characters. Valid Unicode in the path/host is preserved.
func validateUIResourceURI(uri string) error {
	if uri == "" {
		return &InvalidAppResourceError{URI: uri, Reason: "empty URI"}
	}
	for _, r := range uri {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return &InvalidAppResourceError{URI: uri, Reason: "URI contains control characters"}
		}
	}
	u, err := url.Parse(uri)
	if err != nil {
		return &InvalidAppResourceError{URI: uri, Reason: fmt.Sprintf("invalid URI: %v", err)}
	}
	if u.Scheme != "ui" {
		return &InvalidAppResourceError{URI: uri, Reason: "URI scheme must be ui"}
	}
	return nil
}

// AppResourceCSP mirrors the spec's McpUiResourceCsp. JSON keys stay
// camelCase (spec wire format): Phase 1c passes this object verbatim to
// @mcp-ui/client's sandbox.csp prop and Phase 1b's sandbox server parses the
// same shape from the ?csp= query parameter.
type AppResourceCSP struct {
	ConnectDomains  []string `json:"connectDomains,omitempty"`
	ResourceDomains []string `json:"resourceDomains,omitempty"`
	FrameDomains    []string `json:"frameDomains,omitempty"`
	BaseURIDomains  []string `json:"baseUriDomains,omitempty"`
}

// AppResourceUIMeta mirrors the spec's UIResourceMeta (_meta.ui on a
// resources/read result). Permissions is a pass-through map keyed by
// permission name (camera, microphone, geolocation, clipboardWrite) with
// empty-object values, exactly as the spec and @mcp-ui/client define it.
type AppResourceUIMeta struct {
	CSP           *AppResourceCSP           `json:"csp,omitempty"`
	Permissions   map[string]map[string]any `json:"permissions,omitempty"`
	Domain        string                    `json:"domain,omitempty"`
	PrefersBorder *bool                     `json:"prefersBorder,omitempty"`
}

// parseResourceUIMeta extracts _meta.ui from a resources/read content item.
// Malformed values are ignored field-by-field (a bad csp does not discard
// prefersBorder). Returns nil when no ui metadata is present.
func parseResourceUIMeta(meta map[string]any) *AppResourceUIMeta {
	if len(meta) == 0 {
		return nil
	}
	ui, ok := meta["ui"].(map[string]any)
	if !ok {
		return nil
	}

	out := &AppResourceUIMeta{}

	if cspRaw, ok := ui["csp"]; ok {
		if cspMap, ok := cspRaw.(map[string]any); ok {
			data, err := json.Marshal(cspMap)
			if err == nil {
				var csp AppResourceCSP
				if err := json.Unmarshal(data, &csp); err == nil {
					out.CSP = &csp
				}
			}
		}
	}

	if permsRaw, ok := ui["permissions"].(map[string]any); ok {
		perms := make(map[string]map[string]any)
		for k, v := range permsRaw {
			if inner, ok := v.(map[string]any); ok {
				perms[k] = inner
			}
		}
		if len(perms) > 0 {
			out.Permissions = perms
		}
	}

	if s, ok := ui["domain"].(string); ok {
		out.Domain = s
	}

	if b, ok := ui["prefersBorder"].(bool); ok {
		out.PrefersBorder = &b
	}

	if out.CSP == nil && out.Permissions == nil && out.Domain == "" && out.PrefersBorder == nil {
		return nil
	}
	return out
}

// IsUIResourceMIMEType reports whether mimeType is exactly the MCP Apps HTML
// profile (text/html;profile=mcp-app), tolerating parameter whitespace and
// case variations per RFC 2045 via mime.ParseMediaType.
func IsUIResourceMIMEType(mimeType string) bool {
	mt, params, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return false
	}
	return mt == "text/html" && params["profile"] == "mcp-app"
}
