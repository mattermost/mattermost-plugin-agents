// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"io"

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

// readCodespace reads the code ranges of a ToUnicode character map stream.
// The stream has already been charged to the page limits.
func readCodespace(toUnicode pdf.Value) [4][]pdfCodeRange {
	data, err := io.ReadAll(io.LimitReader(toUnicode.Reader(), maxPDFPageStreamBytes))
	if err != nil {
		return [4][]pdfCodeRange{}
	}
	return parseCodespace(data)
}

// parseCodespace returns the ranges of every begincodespacerange section of a
// character map, by code length-1. A range is a pair of equally long strings of
// 1 to 4 bytes. A section with anything but strings in it is malformed and
// ignored; the library's reader rejects the whole map then.
func parseCodespace(data []byte) (codespace [4][]pdfCodeRange) {
	lex := pdfLexer{data: data}
	var operands []string
	inSection, malformed := false, false
	for {
		tok, ok := lex.next()
		if !ok {
			return codespace
		}
		switch {
		case tok.kind == pdfTokenString && inSection:
			operands = append(operands, tok.text)
		case tok.kind == pdfTokenWord && tok.text == "begincodespacerange":
			inSection, malformed, operands = true, false, nil
		case tok.kind == pdfTokenWord && tok.text == "endcodespacerange":
			if inSection && !malformed {
				for i := 0; i+1 < len(operands); i += 2 {
					low, high := operands[i], operands[i+1]
					if len(low) == len(high) && len(low) >= 1 && len(low) <= 4 {
						codespace[len(low)-1] = append(codespace[len(low)-1], pdfCodeRange{low: low, high: high})
					}
				}
			}
			inSection = false
		case inSection:
			malformed = true
		}
	}
}

type pdfTokenKind int

const (
	pdfTokenWord   pdfTokenKind = iota // keyword, number, name or delimiter
	pdfTokenString                     // hex or literal string, decoded in text
)

type pdfToken struct {
	kind pdfTokenKind
	text string
}

// pdfLexer splits PDF or PostScript text into the tokens parseCodespace needs:
// strings decoded to their bytes, and everything else as words. Comments and
// whitespace are skipped; a % inside a string is not a comment.
type pdfLexer struct {
	data []byte
	pos  int
}

func isPDFSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == 0
}

func isPDFDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (l *pdfLexer) next() (pdfToken, bool) {
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		switch {
		case isPDFSpace(c):
			l.pos++
		case c == '%':
			for l.pos < len(l.data) && l.data[l.pos] != '\r' && l.data[l.pos] != '\n' {
				l.pos++
			}
		case c == '(':
			l.pos++
			return pdfToken{pdfTokenString, l.literalString()}, true
		case c == '<' && l.pos+1 < len(l.data) && l.data[l.pos+1] == '<':
			l.pos += 2
			return pdfToken{pdfTokenWord, "<<"}, true
		case c == '<':
			l.pos++
			return pdfToken{pdfTokenString, l.hexString()}, true
		case c == '>' && l.pos+1 < len(l.data) && l.data[l.pos+1] == '>':
			l.pos += 2
			return pdfToken{pdfTokenWord, ">>"}, true
		case isPDFDelimiter(c) && c != '/':
			l.pos++
			return pdfToken{pdfTokenWord, string(c)}, true
		default:
			start := l.pos
			l.pos++
			for l.pos < len(l.data) && !isPDFSpace(l.data[l.pos]) && !isPDFDelimiter(l.data[l.pos]) {
				l.pos++
			}
			return pdfToken{pdfTokenWord, string(l.data[start:l.pos])}, true
		}
	}
	return pdfToken{}, false
}

// hexString reads a hex string after its opening <. Whitespace is ignored and
// a final odd digit counts as followed by 0.
func (l *pdfLexer) hexString() string {
	var out []byte
	var high, have = byte(0), false
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		if c == '>' {
			break
		}
		v, ok := hexDigit(c)
		if !ok {
			continue
		}
		if have {
			out = append(out, high<<4|v)
			have = false
		} else {
			high, have = v, true
		}
	}
	if have {
		out = append(out, high<<4)
	}
	return string(out)
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// literalString reads a literal string after its opening (. Parentheses nest,
// and a backslash starts an escape: a letter escape, up to three octal digits,
// a line continuation, or the escaped character itself.
func (l *pdfLexer) literalString() string {
	var out []byte
	depth := 1
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return string(out)
			}
		case '\\':
			if l.pos >= len(l.data) {
				return string(out)
			}
			e := l.data[l.pos]
			l.pos++
			switch {
			case e >= '0' && e <= '7':
				v := int(e - '0')
				for range 2 {
					if l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '7' {
						v = v*8 + int(l.data[l.pos]-'0')
						l.pos++
					}
				}
				out = append(out, byte(v))
				continue
			case e == 'n':
				out = append(out, '\n')
			case e == 'r':
				out = append(out, '\r')
			case e == 't':
				out = append(out, '\t')
			case e == 'b':
				out = append(out, '\b')
			case e == 'f':
				out = append(out, '\f')
			case e == '\r':
				if l.pos < len(l.data) && l.data[l.pos] == '\n' {
					l.pos++
				}
			case e == '\n':
			default:
				out = append(out, e)
			}
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
