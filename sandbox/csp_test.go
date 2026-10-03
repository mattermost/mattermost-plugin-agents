// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

const testHostOrigin = "https://mm.example.com"

// Spec mandatory omitted-CSP default + permitted further restrictions.
// Written literally — do not derive from BuildCSPHeader.
const specOmittedCSPDefault = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'self'; frame-ancestors https://mm.example.com"

func TestBuildCSPHeader(t *testing.T) {
	tests := []struct {
		name       string
		csp        *mcp.AppResourceCSP
		want       string
		contain    []string
		notContain []string
	}{
		{
			name: "nil ⇒ spec omitted-CSP default",
			csp:  nil,
			want: specOmittedCSPDefault,
		},
		{
			name: "empty declared struct includes font-src",
			csp:  &mcp.AppResourceCSP{},
			want: "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; media-src 'self' data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'self'; frame-ancestors https://mm.example.com",
		},
		{
			name: "connectDomains only",
			csp: &mcp.AppResourceCSP{
				ConnectDomains: []string{"https://api.example.com", "wss://rt.example.com"},
			},
			contain: []string{
				"connect-src 'self' https://api.example.com wss://rt.example.com",
				"font-src 'self';",
			},
			notContain: []string{"font-src 'self' https://"},
		},
		{
			name: "resourceDomains fan-out",
			csp: &mcp.AppResourceCSP{
				ResourceDomains: []string{"https://cdn.example.com"},
			},
			contain: []string{
				"script-src 'self' 'unsafe-inline' https://cdn.example.com",
				"style-src 'self' 'unsafe-inline' https://cdn.example.com",
				"img-src 'self' data: https://cdn.example.com",
				"font-src 'self' https://cdn.example.com",
				"media-src 'self' data: https://cdn.example.com",
				"connect-src 'none'",
				"frame-src 'none'",
			},
		},
		{
			name: "frameDomains replace 'none'",
			csp: &mcp.AppResourceCSP{
				FrameDomains: []string{"https://www.youtube.com"},
			},
			contain:    []string{"frame-src https://www.youtube.com"},
			notContain: []string{"frame-src 'none'"},
		},
		{
			name: "baseUriDomains replace 'self'",
			csp: &mcp.AppResourceCSP{
				BaseURIDomains: []string{"https://cdn.example.com"},
			},
			contain:    []string{"base-uri https://cdn.example.com"},
			notContain: []string{"base-uri 'self'"},
		},
		{
			name: "all four",
			csp: &mcp.AppResourceCSP{
				ConnectDomains:  []string{"https://api.example.com"},
				ResourceDomains: []string{"https://cdn.example.com"},
				FrameDomains:    []string{"https://www.youtube.com"},
				BaseURIDomains:  []string{"https://cdn.example.com"},
			},
			want: "default-src 'none'; script-src 'self' 'unsafe-inline' https://cdn.example.com; style-src 'self' 'unsafe-inline' https://cdn.example.com; img-src 'self' data: https://cdn.example.com; font-src 'self' https://cdn.example.com; media-src 'self' data: https://cdn.example.com; connect-src 'self' https://api.example.com; frame-src https://www.youtube.com; object-src 'none'; base-uri https://cdn.example.com; frame-ancestors https://mm.example.com",
		},
		{
			name: "wildcard subdomain passes",
			csp: &mcp.AppResourceCSP{
				ResourceDomains: []string{"https://*.cloudflare.com"},
			},
			contain: []string{"https://*.cloudflare.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildCSPHeader(tt.csp, testHostOrigin)
			if tt.want != "" {
				require.Equal(t, tt.want, got)
			}
			for _, s := range tt.contain {
				require.Contains(t, got, s)
			}
			for _, s := range tt.notContain {
				require.NotContains(t, got, s)
			}
			if tt.csp == nil {
				require.NotContains(t, got, "font-src")
			}
		})
	}
}

func TestCanonicalizeCSPSource(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "https origin", raw: "https://api.example.com", want: "https://api.example.com"},
		{name: "wss origin", raw: "wss://rt.example.com", want: "wss://rt.example.com"},
		{name: "wildcard", raw: "https://*.cloudflare.com", want: "https://*.cloudflare.com"},
		{name: "star rejected", raw: "*", wantErr: true},
		{name: "data rejected", raw: "data:", wantErr: true},
		{name: "blob rejected", raw: "blob:", wantErr: true},
		{name: "scheme-only rejected", raw: "https:", wantErr: true},
		{name: "userinfo rejected", raw: "https://u:p@evil.com", wantErr: true},
		{name: "path rejected", raw: "https://a.com/path", wantErr: true},
		{name: "query rejected", raw: "https://a.com?x=1", wantErr: true},
		{name: "fragment rejected", raw: "https://a.com#f", wantErr: true},
		{name: "nul rejected", raw: "https://a.com\x00", wantErr: true},
		{name: "vt rejected", raw: "https://a.com\x0b", wantErr: true},
		{name: "crlf rejected", raw: "https://a.com\r\n", wantErr: true},
		{name: "nbsp rejected", raw: "https://a.com\u00a0", wantErr: true},
		{name: "line separator rejected", raw: "https://a.com\u2028", wantErr: true},
		{name: "semicolon rejected", raw: "https://evil.com; script-src *", wantErr: true},
		{name: "mid wildcard rejected", raw: "https://foo.*.bar.com", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalizeCSPSource(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCanonicalizeCSPFailsClosedOnAnyInvalid(t *testing.T) {
	_, err := canonicalizeCSP(&mcp.AppResourceCSP{
		ConnectDomains: []string{"https://ok.example.com", "data:"},
	})
	require.Error(t, err)

	_, err = canonicalizeCSP(&mcp.AppResourceCSP{
		ConnectDomains: make([]string, maxCSPDomains+1),
	})
	require.Error(t, err)
}

func TestParseCSPParam(t *testing.T) {
	fullCSP := mcp.AppResourceCSP{
		ConnectDomains:  []string{"https://a"},
		ResourceDomains: []string{"https://b"},
		FrameDomains:    []string{"https://c"},
		BaseURIDomains:  []string{"https://d"},
	}
	fullJSON, err := json.Marshal(fullCSP)
	require.NoError(t, err)

	oversized := `{"connectDomains":["` + strings.Repeat("x", 9*1024) + `"]}`

	tests := []struct {
		name   string
		raw    string
		want   *mcp.AppResourceCSP
		wantOK bool
	}{
		{name: "empty", raw: "", want: nil, wantOK: true},
		{name: "valid JSON", raw: `{"connectDomains":["https://a"]}`, want: &mcp.AppResourceCSP{ConnectDomains: []string{"https://a"}}, wantOK: true},
		{name: "exactly what AppFrame sends", raw: string(fullJSON), want: &fullCSP, wantOK: true},
		{name: "malformed JSON", raw: `{"connectDomains":`, want: nil, wantOK: false},
		{name: "non-object JSON string", raw: `"str"`, want: nil, wantOK: false},
		{name: "non-object JSON array", raw: `[1,2]`, want: nil, wantOK: false},
		{name: "wrong-typed field", raw: `{"connectDomains":"https://a"}`, want: nil, wantOK: false},
		{name: "unknown extra keys ignored", raw: `{"connectDomains":["https://a"],"future":1}`, want: &mcp.AppResourceCSP{ConnectDomains: []string{"https://a"}}, wantOK: true},
		{name: "oversized", raw: oversized, want: nil, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCSPParam(tt.raw)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
