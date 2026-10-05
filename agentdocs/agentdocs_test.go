// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildPDF returns a minimal one-page PDF whose page content stream is
// content, with a correct cross-reference table.
func buildPDF(content string) []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func TestMimeTypeForName(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
		wantErr  bool
	}{
		{name: "pdf", filename: "handbook.pdf", want: MimeTypePDF},
		{name: "extension is case-insensitive", filename: "HANDBOOK.PDF", want: MimeTypePDF},
		{name: "text", filename: "notes.txt", want: MimeTypeText},
		{name: "markdown", filename: "guide.md", want: MimeTypeMarkdown},
		{name: "long markdown extension", filename: "guide.markdown", want: MimeTypeMarkdown},
		{name: "csv", filename: "data.csv", want: MimeTypeCSV},
		{name: "json", filename: "config.json", want: MimeTypeJSON},
		{name: "office documents are not supported", filename: "report.docx", wantErr: true},
		{name: "no extension", filename: "README", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MimeTypeForName(tt.filename)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidDocument)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.True(t, IsSupportedMimeType(got))
		})
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trims whitespace", input: "  handbook.pdf  ", want: "handbook.pdf"},
		{name: "non-ASCII names are kept", input: "Ünïcode 手册.md", want: "Ünïcode 手册.md"},
		{name: "maximum length", input: strings.Repeat("é", MaxNameRunes), want: strings.Repeat("é", MaxNameRunes)},
		{name: "empty", input: "   ", wantErr: true},
		{name: "too long", input: strings.Repeat("a", MaxNameRunes+1), wantErr: true},
		{name: "slash", input: "../etc/passwd", wantErr: true},
		{name: "backslash", input: `dir\file.txt`, wantErr: true},
		{name: "control character", input: "a\nb.txt", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeName(tt.input)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidDocument)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name      string
		mimeType  string
		data      []byte
		want      string
		wantErrIn string
	}{
		{
			name:     "plain text is trimmed with normalized line endings",
			mimeType: MimeTypeText,
			data:     []byte("\uFEFF  line one\r\nline two\rline three\n\n"),
			want:     "line one\nline two\nline three",
		},
		{
			name:     "markdown keeps its formatting",
			mimeType: MimeTypeMarkdown,
			data:     []byte("# Title\n\n- item\t1\n"),
			want:     "# Title\n\n- item\t1",
		},
		{
			name:     "csv",
			mimeType: MimeTypeCSV,
			data:     []byte("name,role\nAda,engineer\n"),
			want:     "name,role\nAda,engineer",
		},
		{
			name:     "json",
			mimeType: MimeTypeJSON,
			data:     []byte(`{"key": "välue"}`),
			want:     `{"key": "välue"}`,
		},
		{
			name:     "control characters are dropped",
			mimeType: MimeTypeText,
			data:     []byte("bell\a here\x1b"),
			want:     "bell here",
		},
		{
			name:      "invalid UTF-8",
			mimeType:  MimeTypeText,
			data:      []byte{'a', 0xff, 0xfe, 'b'},
			wantErrIn: "not valid UTF-8",
		},
		{
			name:      "binary content in a text file",
			mimeType:  MimeTypeCSV,
			data:      []byte("a,b\x00c"),
			wantErrIn: "not a text file",
		},
		{
			name:      "whitespace-only text",
			mimeType:  MimeTypeMarkdown,
			data:      []byte(" \n\t\r\n"),
			wantErrIn: `no extractable text found in "doc"`,
		},
		{
			name:      "text over the agent budget",
			mimeType:  MimeTypeText,
			data:      []byte(strings.Repeat("a", MaxTotalTextRunes+1)),
			wantErrIn: "more than 100000 characters",
		},
		{
			name:     "pdf text",
			mimeType: MimeTypePDF,
			data:     buildPDF("BT /F1 12 Tf 72 720 Td (Refund policy: 30 days) Tj ET"),
			want:     "Refund policy: 30 days",
		},
		{
			name:      "pdf without text",
			mimeType:  MimeTypePDF,
			data:      buildPDF("0 0 m 100 100 l S"),
			wantErrIn: `no extractable text found in "doc"`,
		},
		{
			name:      "malformed pdf",
			mimeType:  MimeTypePDF,
			data:      []byte("%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\ntrailer << /Root 1 0 R >>\nstartxref\n9999\n%%EOF"),
			wantErrIn: "could not read",
		},
		{
			name:      "truncated pdf",
			mimeType:  MimeTypePDF,
			data:      buildPDF("BT /F1 12 Tf (Hello) Tj ET")[:200],
			wantErrIn: "could not read",
		},
		{
			name:      "content that is not a pdf",
			mimeType:  MimeTypePDF,
			data:      []byte("just text pretending to be a pdf"),
			wantErrIn: "not a PDF file",
		},
		{
			name:      "unsupported type",
			mimeType:  "application/msword",
			data:      []byte("text"),
			wantErrIn: "not a supported document type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Extract("doc", tt.mimeType, tt.data)
			if tt.wantErrIn != "" {
				require.ErrorIs(t, err, ErrInvalidDocument)
				assert.Contains(t, err.Error(), tt.wantErrIn)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractNeverPanicsOnCorruptedPDFs(t *testing.T) {
	valid := buildPDF("BT /F1 12 Tf 72 720 Td (Hello world) Tj ET")
	for cut := 0; cut < len(valid); cut += 7 {
		corrupted := append([]byte(nil), valid...)
		corrupted[cut] ^= 0x5a
		assert.NotPanics(t, func() {
			_, _ = Extract("doc.pdf", MimeTypePDF, corrupted)
		}, "byte %d", cut)
	}
}
