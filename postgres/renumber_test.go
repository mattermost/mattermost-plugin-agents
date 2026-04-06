// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenumberDollarPlaceholders(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		offset   int
		expected string
	}{
		{
			name:     "zero offset returns unchanged",
			sql:      "SELECT * FROM t WHERE a = $1 AND b = $2",
			offset:   0,
			expected: "SELECT * FROM t WHERE a = $1 AND b = $2",
		},
		{
			name:     "offset of 3 shifts all placeholders",
			sql:      "SELECT * FROM t WHERE a = $1 AND b = $2",
			offset:   3,
			expected: "SELECT * FROM t WHERE a = $4 AND b = $5",
		},
		{
			name:     "handles double-digit placeholders",
			sql:      "SELECT * FROM t WHERE a = $1 AND b = $10",
			offset:   5,
			expected: "SELECT * FROM t WHERE a = $6 AND b = $15",
		},
		{
			name:     "no placeholders returns unchanged",
			sql:      "SELECT * FROM t WHERE a = 'hello'",
			offset:   3,
			expected: "SELECT * FROM t WHERE a = 'hello'",
		},
		{
			name:     "single placeholder",
			sql:      "$1",
			offset:   2,
			expected: "$3",
		},
		{
			name:     "placeholders in complex query",
			sql:      "SELECT (e.embedding <-> $1) as sim FROM t WHERE cm.UserId = $2 AND c.DeleteAt = $3",
			offset:   4,
			expected: "SELECT (e.embedding <-> $5) as sim FROM t WHERE cm.UserId = $6 AND c.DeleteAt = $7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renumberDollarPlaceholders(tt.sql, tt.offset)
			assert.Equal(t, tt.expected, result)
		})
	}
}
