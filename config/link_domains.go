// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

const linkDomainWildcardPrefix = "*."

// LinkDomainRule is one canonical entry of the AI-generated link allowlist.
// Host is lowercase ASCII (punycode). A wildcard rule matches any subdomain
// of Host but not Host itself.
type LinkDomainRule struct {
	Host     string `json:"host"`
	Wildcard bool   `json:"wildcard"`
}

// LinkPolicyKind says which AI-generated links the webapp renders as clickable.
type LinkPolicyKind string

const (
	// LinkPolicyPlainText renders external links as plain text.
	LinkPolicyPlainText LinkPolicyKind = "plain_text"
	// LinkPolicyAnyDomain renders every link as clickable.
	LinkPolicyAnyDomain LinkPolicyKind = "any_domain"
	// LinkPolicyAllowlist renders only links to AllowedDomains as clickable.
	LinkPolicyAllowlist LinkPolicyKind = "allowlist"
)

// LinkPolicy is the effective rendering policy for AI-generated links.
type LinkPolicy struct {
	Kind LinkPolicyKind `json:"kind"`
	// AllowedDomains is never nil, so it serializes as a JSON array.
	AllowedDomains []LinkDomainRule `json:"allowedDomains"`
}

// NewLinkPolicy derives the link policy from the AllowUnsafeLinks toggle and
// the comma-separated AllowedLinkDomains setting. Any non-blank setting selects
// the allowlist, so a setting whose entries are all invalid allows no domains
// rather than every domain.
func NewLinkPolicy(allowUnsafeLinks bool, allowedLinkDomains string) LinkPolicy {
	if !allowUnsafeLinks {
		return LinkPolicy{Kind: LinkPolicyPlainText, AllowedDomains: []LinkDomainRule{}}
	}
	rules, invalid := ParseLinkDomains(allowedLinkDomains)
	if len(rules) == 0 && len(invalid) == 0 {
		return LinkPolicy{Kind: LinkPolicyAnyDomain, AllowedDomains: rules}
	}
	return LinkPolicy{Kind: LinkPolicyAllowlist, AllowedDomains: rules}
}

// ParseLinkDomains parses a comma-separated allowlist into canonical rules and
// the entries that are not valid hostnames. Entries may be written as
// hostnames or URLs; schemes, ports, paths, and trailing dots are ignored.
// Blank entries are skipped. The returned rules are never nil.
func ParseLinkDomains(raw string) (rules []LinkDomainRule, invalid []string) {
	rules = []LinkDomainRule{}
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if rule, ok := parseLinkDomain(entry); ok {
			rules = append(rules, rule)
		} else {
			invalid = append(invalid, entry)
		}
	}
	return rules, invalid
}

func parseLinkDomain(entry string) (LinkDomainRule, bool) {
	if !strings.Contains(entry, "://") {
		entry = "http://" + entry
	}
	u, err := url.Parse(entry)
	if err != nil || u.User != nil {
		return LinkDomainRule{}, false
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	wildcard := strings.HasPrefix(host, linkDomainWildcardPrefix)
	host = strings.TrimPrefix(host, linkDomainWildcardPrefix)

	// The lookup profile applies the same UTS 46 mapping browsers use for URL
	// hostnames and rejects characters that cannot appear in one, such as '*'.
	host, err = idna.Lookup.ToASCII(host)
	if err != nil || host == "" {
		return LinkDomainRule{}, false
	}
	return LinkDomainRule{Host: host, Wildcard: wildcard}, true
}
