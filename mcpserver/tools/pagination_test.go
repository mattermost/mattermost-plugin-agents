// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPagination(t *testing.T) {
	tests := []struct {
		name     string
		page     int
		perPage  int
		opts     paginationOptions
		expected pagination
	}{
		{
			name:     "uses explicit values",
			page:     2,
			perPage:  25,
			opts:     paginationOptions{DefaultPerPage: 20, MaxPerPage: 200},
			expected: pagination{Page: 2, PerPage: 25},
		},
		{
			name:     "clamps negative page and uses fallback per page",
			page:     -3,
			perPage:  0,
			opts:     paginationOptions{DefaultPerPage: 20, FallbackPerPage: 35, MaxPerPage: 200},
			expected: pagination{Page: 0, PerPage: 35},
		},
		{
			name:     "uses default per page when explicit and fallback are unset",
			page:     0,
			perPage:  0,
			opts:     paginationOptions{DefaultPerPage: 20, MaxPerPage: 200},
			expected: pagination{Page: 0, PerPage: 20},
		},
		{
			name:     "caps per page",
			page:     0,
			perPage:  500,
			opts:     paginationOptions{DefaultPerPage: 20, MaxPerPage: 200},
			expected: pagination{Page: 0, PerPage: 200},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, newPagination(tt.page, tt.perPage, tt.opts))
		})
	}
}

func TestPaginationSliceBounds(t *testing.T) {
	tests := []struct {
		name       string
		pagination pagination
		total      int
		start      int
		end        int
		ok         bool
	}{
		{
			name:       "returns requested page bounds",
			pagination: pagination{Page: 1, PerPage: 4},
			total:      10,
			start:      4,
			end:        8,
			ok:         true,
		},
		{
			name:       "caps final partial page",
			pagination: pagination{Page: 2, PerPage: 4},
			total:      10,
			start:      8,
			end:        10,
			ok:         true,
		},
		{
			name:       "reports out of range without multiplying huge page",
			pagination: pagination{Page: math.MaxInt, PerPage: 4},
			total:      10,
			ok:         false,
		},
		{
			name:       "returns full range when per page is unset",
			pagination: pagination{Page: 0, PerPage: 0},
			total:      10,
			start:      0,
			end:        10,
			ok:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, ok := tt.pagination.SliceBounds(tt.total)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.start, start)
			assert.Equal(t, tt.end, end)
		})
	}
}
