// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

const (
	// maxCSPParamBytes caps the raw ?csp= value; larger inputs are treated
	// as malformed (fail closed to the restrictive default).
	maxCSPParamBytes = 8 * 1024
	// maxCSPDomains / maxCSPDomainLen bound each domain list.
	maxCSPDomains   = 32
	maxCSPDomainLen = 256
)

// parseCSPParam parses the raw ?csp= query value (the JSON-serialized
// McpUiResourceCsp that @mcp-ui/client's AppFrame appends to the sandbox
// URL). Returns (nil, false) for malformed or oversized input — callers
// fail closed to the restrictive default. Returns (nil, true) when raw is
// empty (no CSP declared).
func parseCSPParam(raw string) (*mcp.AppResourceCSP, bool) {
	if raw == "" {
		return nil, true
	}
	if len(raw) > maxCSPParamBytes {
		return nil, false
	}
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var csp mcp.AppResourceCSP
	if err := json.Unmarshal([]byte(raw), &csp); err != nil {
		return nil, false
	}
	return &csp, true
}

// canonicalizeCSPSource parses and canonicalizes a single CSP source that
// must be a browser origin (or https?://*.example.com wildcard subdomain).
// Rejects control/Unicode-separator characters, userinfo, paths, query,
// fragment, scheme-only sources, `*`, `data:`, and `blob:`.
func canonicalizeCSPSource(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty CSP source")
	}
	if len(raw) > maxCSPDomainLen {
		return "", fmt.Errorf("CSP source exceeds length cap")
	}
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("CSP source is not valid UTF-8")
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f || unicode.Is(unicode.C, r) || unicode.Is(unicode.Z, r) {
			return "", fmt.Errorf("CSP source contains control or separator characters")
		}
		if r == ';' || r == ',' || r == '\'' || r == '"' {
			return "", fmt.Errorf("CSP source contains forbidden punctuation")
		}
	}
	switch raw {
	case "*", "data:", "blob:", "http:", "https:", "ws:", "wss:":
		return "", fmt.Errorf("CSP source %q is not an allowed origin expression", raw)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("CSP source is not a URL: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return "", fmt.Errorf("CSP source scheme must be http(s) or ws(s)")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("CSP source must not include userinfo")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("CSP source must not include query or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("CSP source must not include a path")
	}
	host := parsed.Host
	if host == "" {
		return "", fmt.Errorf("CSP source must include a host")
	}
	// Allow a single leading "*." wildcard label (spec resourceDomains).
	hostname := parsed.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("CSP source must include a host")
	}
	if strings.Contains(hostname, "*") {
		if !strings.HasPrefix(hostname, "*.") || strings.Count(hostname, "*") != 1 {
			return "", fmt.Errorf("CSP source wildcard must be a single leading *. label")
		}
		rest := hostname[2:]
		if rest == "" || strings.Contains(rest, "*") {
			return "", fmt.Errorf("CSP source wildcard must be a single leading *. label")
		}
	}

	// Canonical form: scheme://host exactly as parsed (preserve port / *. ).
	out := parsed.Scheme + "://" + host
	return out, nil
}

// canonicalizeCSPDomains validates every entry; any invalid source fails the
// whole list. Caps are hard limits — exceeding them is an error (fail closed).
func canonicalizeCSPDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	if len(domains) > maxCSPDomains {
		return nil, fmt.Errorf("CSP source list exceeds %d entries", maxCSPDomains)
	}
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		canon, err := canonicalizeCSPSource(d)
		if err != nil {
			return nil, err
		}
		out = append(out, canon)
	}
	return out, nil
}

// canonicalizeCSP validates and canonicalizes all domain lists on csp.
// Any invalid source returns an error (caller fails closed to restrictive default).
func canonicalizeCSP(csp *mcp.AppResourceCSP) (*mcp.AppResourceCSP, error) {
	if csp == nil {
		return nil, nil
	}
	connect, err := canonicalizeCSPDomains(csp.ConnectDomains)
	if err != nil {
		return nil, fmt.Errorf("connectDomains: %w", err)
	}
	resource, err := canonicalizeCSPDomains(csp.ResourceDomains)
	if err != nil {
		return nil, fmt.Errorf("resourceDomains: %w", err)
	}
	frame, err := canonicalizeCSPDomains(csp.FrameDomains)
	if err != nil {
		return nil, fmt.Errorf("frameDomains: %w", err)
	}
	baseURI, err := canonicalizeCSPDomains(csp.BaseURIDomains)
	if err != nil {
		return nil, fmt.Errorf("baseUriDomains: %w", err)
	}
	return &mcp.AppResourceCSP{
		ConnectDomains:  connect,
		ResourceDomains: resource,
		FrameDomains:    frame,
		BaseURIDomains:  baseURI,
	}, nil
}

// BuildCSPHeader constructs the Content-Security-Policy header value for a
// sandbox page response per the MCP Apps spec (2026-01-26). A nil csp
// produces the spec's mandatory omitted-CSP restrictive default plus only
// further restrictions (object-src, frame-src, base-uri, frame-ancestors).
// A non-nil csp uses the declared-CSP construction template (includes
// font-src). hostOrigin is pinned as the only allowed frame-ancestor.
// Callers must pass already-canonicalized domain lists (or nil).
func BuildCSPHeader(csp *mcp.AppResourceCSP, hostOrigin string) string {
	if csp == nil {
		// Spec mandatory omitted-CSP default + permitted further restrictions.
		// Notably: NO font-src (fonts fall under default-src 'none').
		return strings.Join([]string{
			"default-src 'none'",
			"script-src 'self' 'unsafe-inline'",
			"style-src 'self' 'unsafe-inline'",
			"img-src 'self' data:",
			"media-src 'self' data:",
			"connect-src 'none'",
			"frame-src 'none'",
			"object-src 'none'",
			"base-uri 'self'",
			"frame-ancestors " + hostOrigin,
		}, "; ")
	}

	resourceSuffix := joinCSPSources(csp.ResourceDomains)
	connectSrc := "'none'"
	if len(csp.ConnectDomains) > 0 {
		connectSrc = "'self'" + joinCSPSources(csp.ConnectDomains)
	}
	frameSrc := "'none'"
	if len(csp.FrameDomains) > 0 {
		frameSrc = strings.Join(csp.FrameDomains, " ")
	}
	baseURISrc := "'self'"
	if len(csp.BaseURIDomains) > 0 {
		baseURISrc = strings.Join(csp.BaseURIDomains, " ")
	}

	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'self' 'unsafe-inline'" + resourceSuffix,
		"style-src 'self' 'unsafe-inline'" + resourceSuffix,
		"img-src 'self' data:" + resourceSuffix,
		"font-src 'self'" + resourceSuffix,
		"media-src 'self' data:" + resourceSuffix,
		"connect-src " + connectSrc,
		"frame-src " + frameSrc,
		"object-src 'none'",
		"base-uri " + baseURISrc,
		"frame-ancestors " + hostOrigin,
	}, "; ")
}

// joinCSPSources returns a leading-space-prefixed space-joined list, or ""
// when domains is empty.
func joinCSPSources(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	return " " + strings.Join(domains, " ")
}
