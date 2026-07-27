// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"testing"
)

func FuzzCanonicalizeCSPSource(f *testing.F) {
	seeds := []string{
		"https://api.example.com",
		"wss://rt.example.com",
		"https://*.cloudflare.com",
		"*",
		"data:",
		"blob:",
		"https:",
		"https://u:p@evil.com",
		"https://a.com/path",
		"https://a.com?x=1",
		"https://a.com#f",
		"https://a.com\x00",
		"https://a.com\x0b",
		"https://a.com\r\n",
		"https://a.com\u00a0",
		"https://evil.com; script-src *",
		"'unsafe-eval'",
		"",
		"http://localhost:8065",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		canon, err := canonicalizeCSPSource(raw)
		if err != nil {
			return
		}
		// Successful canonicalization must round-trip through the same validator
		// and never contain control characters or forbidden punctuation.
		again, err2 := canonicalizeCSPSource(canon)
		if err2 != nil {
			t.Fatalf("canonical form %q rejected: %v", canon, err2)
		}
		if again != canon {
			t.Fatalf("canonical form not stable: %q -> %q", canon, again)
		}
		for _, r := range canon {
			if r < 0x20 || r == 0x7f || r == ';' || r == ',' || r == '\'' || r == '"' {
				t.Fatalf("canonical form %q contains forbidden rune %U", canon, r)
			}
		}
	})
}
