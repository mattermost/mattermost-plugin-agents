// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCodespace(t *testing.T) {
	threeBytes := [4][]pdfCodeRange{2: {{low: "\x00\x00\x00", high: "\xff\xff\xff"}}}

	tests := []struct {
		name string
		cmap string
		want [4][]pdfCodeRange
	}{
		{
			name: "hex strings",
			cmap: "1 begincodespacerange <000000> <FFFFFF> endcodespacerange",
			want: threeBytes,
		},
		{
			name: "hex strings with whitespace and lowercase digits",
			cmap: "1 begincodespacerange <00 00\n00> <ff ff ff> endcodespacerange",
			want: threeBytes,
		},
		{
			name: "literal strings with octal escapes",
			cmap: `1 begincodespacerange (\000\000\000) (\377\377\377) endcodespacerange`,
			want: threeBytes,
		},
		{
			name: "literal strings with letter escapes, short octal escapes and nested parentheses",
			cmap: `2 begincodespacerange (\0\n) (\377\)) (a(b)) (c(d)) endcodespacerange`,
			want: [4][]pdfCodeRange{
				1: {{low: "\x00\n", high: "\xff)"}},
				3: {{low: "a(b)", high: "c(d)"}},
			},
		},
		{
			name: "comments between operands and after the section",
			cmap: "1 begincodespacerange <000000> % lower bound\n<FFFFFF> % upper bound\nendcodespacerange % done",
			want: threeBytes,
		},
		{
			name: "percent signs inside strings are not comments",
			cmap: "/CMapName (100% complete) def 1 begincodespacerange (\\000\\000\\000) (\\377\\377\\377) endcodespacerange",
			want: threeBytes,
		},
		{
			name: "mixed forms and widths in one section",
			cmap: "2 begincodespacerange <00> (\\177) <8000> % wide\n(\\377\\377) endcodespacerange",
			want: [4][]pdfCodeRange{
				0: {{low: "\x00", high: "\x7f"}},
				1: {{low: "\x80\x00", high: "\xff\xff"}},
			},
		},
		{
			name: "a section that is not a pair of equal-length bounds is skipped",
			cmap: "2 begincodespacerange <00> <FFFF> <0000> <FF> endcodespacerange",
			want: [4][]pdfCodeRange{},
		},
		{
			name: "a section with a non-string operand is malformed",
			cmap: "1 begincodespacerange <00> 12 <FF> endcodespacerange",
			want: [4][]pdfCodeRange{},
		},
		{
			name: "bounds longer than four bytes are ignored",
			cmap: "1 begincodespacerange <0000000000> <FFFFFFFFFF> endcodespacerange",
			want: [4][]pdfCodeRange{},
		},
		{
			name: "strings outside a section are ignored",
			cmap: "<000000> <FFFFFF> 1 beginbfchar <01> <0041> endbfchar",
			want: [4][]pdfCodeRange{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseCodespace([]byte(tt.cmap)))
		})
	}
}
