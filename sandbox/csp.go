// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

const (
	// maxCSPParamBytes caps the raw ?csp= value; larger inputs are treated
	// as malformed (fail closed to the restrictive default).
	maxCSPParamBytes = 8 * 1024
	// maxCSPDomains / maxCSPDomainLen bound each domain list; entries
	// beyond the caps are dropped.
	maxCSPDomains   = 32
	maxCSPDomainLen = 256
)

// cspDomainDenied rejects entries that could break out of a CSP source list:
// ';' and newlines start a new directive, quotes inject CSP keywords,
// whitespace injects extra sources, ',' splits headers.
var cspDomainDenied = regexp.MustCompile(`[;,'"\s]`)

// sanitizeCSPDomains returns the entries of domains that are safe to embed
// in a CSP source list, applying the count/length caps.
func sanitizeCSPDomains(domains []string) []string {
	if len(domains) == 0 {
		return nil
	}
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		if len(out) >= maxCSPDomains {
			break
		}
		if d == "" || len(d) > maxCSPDomainLen || cspDomainDenied.MatchString(d) {
			continue
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

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
	// Reject non-object JSON (string/array) before unmarshaling: a JSON
	// null would otherwise succeed into the zero-value struct.
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

// BuildCSPHeader constructs the Content-Security-Policy header value for a
// sandbox page response per the MCP Apps spec (2026-01-26). A nil csp
// produces the spec's restrictive default. hostOrigin is additionally
// pinned as the only allowed frame-ancestor.
func BuildCSPHeader(csp *mcp.AppResourceCSP, hostOrigin string) string {
	var connect, resource, frame, baseURI []string
	if csp != nil {
		connect = sanitizeCSPDomains(csp.ConnectDomains)
		resource = sanitizeCSPDomains(csp.ResourceDomains)
		frame = sanitizeCSPDomains(csp.FrameDomains)
		baseURI = sanitizeCSPDomains(csp.BaseURIDomains)
	}

	resourceSuffix := joinCSPSources(resource)
	connectSrc := "'none'"
	if len(connect) > 0 {
		connectSrc = "'self'" + joinCSPSources(connect)
	}
	frameSrc := "'none'"
	if len(frame) > 0 {
		frameSrc = strings.TrimSpace(joinCSPSources(frame))
	}
	baseURISrc := "'self'"
	if len(baseURI) > 0 {
		baseURISrc = strings.TrimSpace(joinCSPSources(baseURI))
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
