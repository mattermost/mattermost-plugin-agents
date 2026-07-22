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

func TestBuildCSPHeader(t *testing.T) {
	restrictiveDefault := "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; media-src 'self' data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'self'; frame-ancestors https://mm.example.com"

	longDomain := "https://" + strings.Repeat("a", 300) + ".example.com"
	manyDomains := make([]string, 40)
	for i := range manyDomains {
		manyDomains[i] = "https://d" + strings.Repeat("x", i%5) + ".example.com"
	}

	tests := []struct {
		name string
		csp  *mcp.AppResourceCSP
		want string
		// contain / notContain used when full-string equality is awkward
		contain    []string
		notContain []string
	}{
		{
			name: "nil ⇒ restrictive default",
			csp:  nil,
			want: restrictiveDefault,
		},
		{
			name: "empty struct ⇒ same as nil",
			csp:  &mcp.AppResourceCSP{},
			want: restrictiveDefault,
		},
		{
			name: "connectDomains only",
			csp: &mcp.AppResourceCSP{
				ConnectDomains: []string{"https://api.example.com", "wss://rt.example.com"},
			},
			contain: []string{
				"connect-src 'self' https://api.example.com wss://rt.example.com",
				"script-src 'self' 'unsafe-inline';",
				"style-src 'self' 'unsafe-inline';",
				"img-src 'self' data:;",
			},
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
			notContain: []string{
				"connect-src 'self' https://cdn.example.com",
				"frame-src https://cdn.example.com",
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
		{
			name: "directive injection filtered",
			csp: &mcp.AppResourceCSP{
				ConnectDomains: []string{
					"https://ok.example.com",
					"https://evil.com; script-src *",
					"'unsafe-eval'",
					"a b",
					"bad,domain",
					"line\nbreak",
				},
			},
			contain:    []string{"connect-src 'self' https://ok.example.com"},
			notContain: []string{"evil.com", "unsafe-eval", "bad,domain"},
		},
		{
			name: "all entries filtered ⇒ restrictive branch",
			csp: &mcp.AppResourceCSP{
				ConnectDomains: []string{"bad domain"},
			},
			contain: []string{"connect-src 'none'"},
		},
		{
			name: "caps enforced",
			csp: &mcp.AppResourceCSP{
				ConnectDomains: append(append([]string{}, manyDomains...), longDomain),
			},
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
			if tt.name == "caps enforced" {
				// Extract connect-src list and assert ≤32 entries; long domain dropped.
				require.NotContains(t, got, longDomain)
				idx := strings.Index(got, "connect-src ")
				require.GreaterOrEqual(t, idx, 0)
				rest := got[idx+len("connect-src "):]
				semi := strings.Index(rest, ";")
				require.GreaterOrEqual(t, semi, 0)
				sources := strings.Fields(rest[:semi])
				// sources[0] is 'self'
				require.LessOrEqual(t, len(sources)-1, 32)
				require.Equal(t, 32, len(sources)-1)
			}
		})
	}
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
		{
			name:   "empty",
			raw:    "",
			want:   nil,
			wantOK: true,
		},
		{
			name:   "valid JSON",
			raw:    `{"connectDomains":["https://a"]}`,
			want:   &mcp.AppResourceCSP{ConnectDomains: []string{"https://a"}},
			wantOK: true,
		},
		{
			name:   "exactly what AppFrame sends",
			raw:    string(fullJSON),
			want:   &fullCSP,
			wantOK: true,
		},
		{
			name:   "malformed JSON",
			raw:    `{"connectDomains":`,
			want:   nil,
			wantOK: false,
		},
		{
			name:   "non-object JSON string",
			raw:    `"str"`,
			want:   nil,
			wantOK: false,
		},
		{
			name:   "non-object JSON array",
			raw:    `[1,2]`,
			want:   nil,
			wantOK: false,
		},
		{
			name:   "wrong-typed field",
			raw:    `{"connectDomains":"https://a"}`,
			want:   nil,
			wantOK: false,
		},
		{
			name:   "unknown extra keys ignored",
			raw:    `{"connectDomains":["https://a"],"future":1}`,
			want:   &mcp.AppResourceCSP{ConnectDomains: []string{"https://a"}},
			wantOK: true,
		},
		{
			name:   "oversized",
			raw:    oversized,
			want:   nil,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCSPParam(tt.raw)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
