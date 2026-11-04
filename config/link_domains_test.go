// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseLinkDomains(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantRules   []LinkDomainRule
		wantInvalid []string
	}{
		{name: "empty", raw: "", wantRules: []LinkDomainRule{}},
		{name: "blank entries are skipped", raw: " , ,, ", wantRules: []LinkDomainRule{}},
		{name: "plain host", raw: "example.com", wantRules: []LinkDomainRule{{Host: "example.com"}}},
		{name: "wildcard host", raw: "*.example.com", wantRules: []LinkDomainRule{{Host: "example.com", Wildcard: true}}},
		{
			name:      "trims, lowercases, and strips trailing dots",
			raw:       " Example.COM , *.Mattermost.com. ",
			wantRules: []LinkDomainRule{{Host: "example.com"}, {Host: "mattermost.com", Wildcard: true}},
		},
		{
			name:      "URL entries keep only the hostname",
			raw:       "https://docs.example.com:8443/path?q=1, example.org:8080/x",
			wantRules: []LinkDomainRule{{Host: "docs.example.com"}, {Host: "example.org"}},
		},
		{name: "unicode hosts become punycode", raw: "Bücher.example", wantRules: []LinkDomainRule{{Host: "xn--bcher-kva.example"}}},
		{
			name:        "invalid entries are reported and dropped",
			raw:         "example.com, *, *., exa*mple.com, bad host.com, user@example.net",
			wantRules:   []LinkDomainRule{{Host: "example.com"}},
			wantInvalid: []string{"*", "*.", "exa*mple.com", "bad host.com", "user@example.net"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, invalid := ParseLinkDomains(tt.raw)
			assert.Equal(t, tt.wantRules, rules)
			assert.Equal(t, tt.wantInvalid, invalid)
		})
	}
}

func TestNewLinkPolicy(t *testing.T) {
	tests := []struct {
		name               string
		allowUnsafeLinks   bool
		allowedLinkDomains string
		want               LinkPolicy
	}{
		{
			name:               "links disabled ignores the allowlist",
			allowUnsafeLinks:   false,
			allowedLinkDomains: "example.com",
			want:               LinkPolicy{Kind: LinkPolicyPlainText, AllowedDomains: []LinkDomainRule{}},
		},
		{
			name:             "links enabled without an allowlist allows any domain",
			allowUnsafeLinks: true,
			want:             LinkPolicy{Kind: LinkPolicyAnyDomain, AllowedDomains: []LinkDomainRule{}},
		},
		{
			name:               "blank allowlist allows any domain",
			allowUnsafeLinks:   true,
			allowedLinkDomains: " , ",
			want:               LinkPolicy{Kind: LinkPolicyAnyDomain, AllowedDomains: []LinkDomainRule{}},
		},
		{
			name:               "allowlist narrows links to its domains",
			allowUnsafeLinks:   true,
			allowedLinkDomains: "example.com, *.mattermost.com",
			want: LinkPolicy{Kind: LinkPolicyAllowlist, AllowedDomains: []LinkDomainRule{
				{Host: "example.com"},
				{Host: "mattermost.com", Wildcard: true},
			}},
		},
		{
			name:               "allowlist of only invalid entries allows no domains",
			allowUnsafeLinks:   true,
			allowedLinkDomains: "*, not a host",
			want:               LinkPolicy{Kind: LinkPolicyAllowlist, AllowedDomains: []LinkDomainRule{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NewLinkPolicy(tt.allowUnsafeLinks, tt.allowedLinkDomains))
		})
	}
}
