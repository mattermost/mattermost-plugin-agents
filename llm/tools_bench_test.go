// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"
)

var benchStrings = []string{
	// Typical JSON argument - all ASCII
	`{"url": "https://example.com/api/v1/users?page=1&limit=100", "method": "GET", "headers": {"Authorization": "Bearer token123"}}`,
	// Longer ASCII string
	`{"query": "SELECT id, name, email, created_at, updated_at FROM users WHERE status = 'active' AND role IN ('admin', 'user') ORDER BY created_at DESC LIMIT 50"}`,
	// String with unicode (but safe)
	`{"message": "Hello 世界! Welcome to our platform 🎉"}`,
	// String with problematic chars
	"https://good.com\u2067@evil.com/path",
}

func BenchmarkSanitizeNonPrintableChars(b *testing.B) {
	for i, s := range benchStrings {
		b.Run(string(rune('A'+i)), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = SanitizeNonPrintableChars(s)
			}
		})
	}
}
