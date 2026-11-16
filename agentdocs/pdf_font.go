// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"encoding/hex"
	"io"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

// pdfFontEncoding is a font's text encoding together with the lengths of its
// character codes. The library decodes a font's ToUnicode character map one
// code of 1 to 4 bytes at a time, so a chunk handed to it must end on a code
// boundary.
type pdfFontEncoding struct {
	enc pdf.TextEncoding
	// codespace holds the character map's code ranges by length-1. It is empty
	// for encodings with one-byte codes, which the library decodes byte by byte.
	codespace [4][]pdfCodeRange
}

type pdfCodeRange struct{ low, high string }

// codeLen returns the length of the character code at the start of raw, which
// follows the library: the shortest length whose code falls in a range, or one
// byte when no range matches.
func (e *pdfFontEncoding) codeLen(raw string) int {
	for n := 1; n <= 4 && n <= len(raw); n++ {
		for _, r := range e.codespace[n-1] {
			if r.low <= raw[:n] && raw[:n] <= r.high {
				return n
			}
		}
	}
	return 1
}

// chunkLen returns how much of raw to decode next: whole character codes,
// about pdfDecodeChunkBytes bytes of them.
func (e *pdfFontEncoding) chunkLen(raw string) int {
	n := 0
	for n < len(raw) && n < pdfDecodeChunkBytes {
		n += e.codeLen(raw[n:])
	}
	return n
}

// usesCharacterMap reports whether the library decodes font through its
// ToUnicode character map.
func usesCharacterMap(font pdf.Value) bool {
	enc := font.Key("Encoding")
	return enc.IsNull() || (enc.Kind() == pdf.Name && enc.Name() == "Identity-H")
}

var (
	codespaceBlock = regexp.MustCompile(`(?s)begincodespacerange(.*?)endcodespacerange`)
	codespacePair  = regexp.MustCompile(`<([0-9A-Fa-f\s]*)>\s*<([0-9A-Fa-f\s]*)>`)
)

// readCodespace reads the code ranges of a ToUnicode character map stream.
// Comments, which the library's tokenizer allows between operands, are
// dropped first; a % inside a string is not a comment. The stream has already been charged to the page limits.
func readCodespace(toUnicode pdf.Value) (codespace [4][]pdfCodeRange) {
	data, err := io.ReadAll(io.LimitReader(toUnicode.Reader(), maxPDFPageStreamBytes))
	if err != nil {
		return codespace
	}
	data = stripPDFComments(data)
	for _, block := range codespaceBlock.FindAllSubmatch(data, -1) {
		for _, pair := range codespacePair.FindAllSubmatch(block[1], -1) {
			low, lowOK := decodeHexString(pair[1])
			high, highOK := decodeHexString(pair[2])
			if !lowOK || !highOK || len(low) != len(high) || len(low) < 1 || len(low) > 4 {
				continue
			}
			codespace[len(low)-1] = append(codespace[len(low)-1], pdfCodeRange{low: low, high: high})
		}
	}
	return codespace
}

func decodeHexString(s []byte) (string, bool) {
	clean := strings.Join(strings.Fields(string(s)), "")
	if len(clean)%2 != 0 {
		return "", false
	}
	decoded, err := hex.DecodeString(clean)
	return string(decoded), err == nil
}

// stripPDFComments removes the comments from PDF or PostScript text: a %
// outside a string, up to the end of its line. Literal strings, which nest
// parentheses and escape with a backslash, and hex strings are kept whole.
func stripPDFComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		switch c := data[i]; c {
		case '%':
			for i < len(data) && data[i] != '\r' && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, data[i])
			}
		case '(':
			start, depth := i, 0
			for ; i < len(data); i++ {
				if data[i] == '\\' {
					i++
				} else if data[i] == '(' {
					depth++
				} else if data[i] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			out = append(out, data[start:min(i+1, len(data))]...)
		default:
			out = append(out, c)
		}
	}
	return out
}
