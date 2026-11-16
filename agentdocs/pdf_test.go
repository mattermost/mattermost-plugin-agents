// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flateStream returns a FlateDecode stream object holding data.
func flateStream(t *testing.T, data string) string {
	t.Helper()
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, err := w.Write([]byte(data))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return pdfStream("/Filter /FlateDecode", b.String())
}

const helveticaFont = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"

// textPage returns a page object under parent whose contents are object
// contents, without resources of its own.
func textPage(parent, contents int) string {
	return fmt.Sprintf("<< /Type /Page /Parent %d 0 R /Contents %d 0 R >>", parent, contents)
}

func refs(id, n int) string {
	return strings.TrimSpace(strings.Repeat(fmt.Sprintf("%d 0 R ", id), n))
}

// nestedPagesPDF returns a PDF whose only page is under depth nested page
// tree nodes.
func nestedPagesPDF(depth int) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>"}
	for i := range depth {
		id := i + 2
		objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", id+1))
	}
	pageID := depth + 2
	objects = append(objects,
		fmt.Sprintf("<< /Type /Page /Parent %d 0 R /Contents %d 0 R >>", pageID-1, pageID+1),
		pdfStream("", "BT (Deep page) Tj ET"),
	)
	return assemblePDF(objects...)
}

// toUnicodeFont returns a font whose text is decoded by the character map
// in object cmap.
func toUnicodeFont(cmap int) string {
	return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /Test /ToUnicode %d 0 R >>", cmap)
}

// bfchars returns a ToUnicode character map with the given code space ranges
// ("<lo> <hi>" pairs) and one-code mappings from hex code to letter.
func bfchars(codespace string, mappings ...[2]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d begincodespacerange %s endcodespacerange\n%d beginbfchar\n", strings.Count(codespace, "<")/2, codespace, len(mappings))
	for _, m := range mappings {
		fmt.Fprintf(&b, "<%s> <%04X>\n", m[0], []rune(m[1])[0])
	}
	b.WriteString("endbfchar")
	return b.String()
}

// pageWithResources returns a page object under parent with contents in
// object contents and the given /Resources entries.
func pageWithResources(parent, contents int, resources string) string {
	return fmt.Sprintf("<< /Type /Page /Parent %d 0 R /Contents %d 0 R /Resources << %s >> >>", parent, contents, resources)
}

func formXObject(resources, content string) string {
	return pdfStream("/Type /XObject /Subtype /Form /BBox [0 0 100 100] "+resources, content)
}

func TestExtractPDF(t *testing.T) {
	tests := []struct {
		name      string
		data      func(t *testing.T) []byte
		want      string
		wantErrIn string
	}{
		{
			name: "pages of a nested page tree are read in order with inherited resources",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R 6 0 R] /Count 3 /Resources << /Font << /F1 9 0 R >> >> >>",
					"<< /Type /Pages /Parent 2 0 R /Kids [4 0 R 5 0 R] /Count 2 >>",
					textPage(3, 7),
					"<< /Type /Page /Parent 3 0 R /Contents [8 0 R 10 0 R] >>",
					textPage(2, 11),
					pdfStream("", "BT /F1 12 Tf (Page one) Tj ET"),
					pdfStream("", "BT /F1 12 Tf (Page two, ) Tj\n"),
					helveticaFont,
					pdfStream("", "[(continued) -250 (\\222s) ] TJ ET"),
					pdfStream("", "BT /F1 12 Tf (Page three) Tj ET"),
				)
			},
			want: "Page one\n\nPage two, continued’s\n\nPage three",
		},
		{
			name: "text positioning separates lines and words",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					textPage(2, 4),
					pdfStream("", "BT (Refund) Tj 0 -14 Td (policy) Tj 40 0 Td (applies) Tj 0 -14 TD (here) Tj 1 0 0 1 10 100 Tm (and) Tj 1 0 0 1 60 100 Tm (there) Tj 1 0 0 1 10 80 Tm (end) Tj ET"),
				)
			},
			want: "Refund\npolicy applies\nhere\nand there\nend",
		},
		{
			name: "text in a form XObject is read where the form is drawn",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/Font << /F1 5 0 R >> /XObject << /Fm1 6 0 R >>"),
					pdfStream("", "BT /F1 12 Tf (Before) Tj ET q /Fm1 Do Q BT /F1 12 Tf (After) Tj ET"),
					helveticaFont,
					formXObject("/Resources << /Font << /F1 5 0 R /F2 8 0 R >> /XObject << /Inner 7 0 R >> >>", "BT /F1 12 Tf (Refund policy) Tj ET /Inner Do"),
					formXObject("", "BT /F1 12 Tf (Nested form) Tj ET"),
					helveticaFont,
				)
			},
			want: "Before\nRefund policy\nNested form\nAfter",
		},
		{
			name: "form XObject uses the font selected where it is drawn, and its own font changes stay inside it",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/Font << /F1 5 0 R >> /XObject << /Fm1 7 0 R >>"),
					pdfStream("", "BT /F1 12 Tf ET /Fm1 Do BT (\x01) Tj ET"),
					toUnicodeFont(6),
					pdfStream("", bfchars("<00> <FF>", [2]string{"01", "A"})),
					formXObject("/Resources << /Font << /F2 8 0 R >> >>", "BT (\x01) Tj /F2 12 Tf (\x01) Tj ET"),
					toUnicodeFont(9),
					pdfStream("", bfchars("<00> <FF>", [2]string{"01", "B"})),
				)
			},
			want: "AB\nA",
		},
		{
			name: "form XObject that draws itself",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/XObject << /Fm1 5 0 R >>"),
					pdfStream("", "/Fm1 Do"),
					formXObject("/Resources << /XObject << /Fm1 5 0 R >> >>", "BT (Again) Tj ET /Fm1 Do"),
				)
			},
			wantErrIn: `page 1 of "doc.pdf" is too complex to read`,
		},
		{
			name: "form XObject drawn many times",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/XObject << /Fm1 5 0 R >>"),
					pdfStream("", strings.Repeat("/Fm1 Do\n", maxPDFForms+1)),
					formXObject("", "BT (x) Tj ET"),
				)
			},
			wantErrIn: `page 1 of "doc.pdf" is too complex to read`,
		},
		{
			name: "font restored by Q",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/Font << /F1 5 0 R /F2 6 0 R >>"),
					pdfStream("", "BT /F1 12 Tf (\x01) Tj ET q BT /F2 12 Tf (\x01) Tj ET Q BT (\x01) Tj ET"),
					toUnicodeFont(7),
					toUnicodeFont(8),
					pdfStream("", bfchars("<00> <FF>", [2]string{"01", "A"})),
					pdfStream("", bfchars("<00> <FF>", [2]string{"01", "B"})),
				)
			},
			want: "A\nB\nA",
		},
		{
			name: "three-byte font codes across decoding chunks",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/Font << /F1 5 0 R >>"),
					pdfStream("", "BT /F1 12 Tf ("+strings.Repeat("\x01\x02\x03\x04\x05\x06", 5)+") Tj ET"),
					toUnicodeFont(6),
					pdfStream("", bfchars("<000000> % lower bound\n<FFFFFF>", [2]string{"010203", "A"}, [2]string{"040506", "B"})),
				)
			},
			want: "ABABABABAB",
		},
		{
			name: "percent sign inside a character map string is not a comment",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/Font << /F1 5 0 R >>"),
					pdfStream("", "BT /F1 12 Tf ("+strings.Repeat("\x01\x02\x03\x04\x05\x06", 5)+") Tj ET"),
					toUnicodeFont(6),
					pdfStream("", "/CMapName (100% complete) def "+bfchars("<000000> <FFFFFF>", [2]string{"010203", "A"}, [2]string{"040506", "B"})),
				)
			},
			want: "ABABABABAB",
		},
		{
			name: "three-byte font codes with literal string bounds across decoding chunks",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/Font << /F1 5 0 R >>"),
					pdfStream("", "BT /F1 12 Tf ("+strings.Repeat("\x01\x02\x03\x04\x05\x06", 5)+") Tj ET"),
					toUnicodeFont(6),
					pdfStream("", "1 begincodespacerange (\\000\\000\\000) (\\377\\377\\377) endcodespacerange\n2 beginbfchar\n<010203> <0041>\n<040506> <0042>\nendbfchar"),
				)
			},
			want: "ABABABABAB",
		},
		{
			name: "font codes of mixed widths across decoding chunks",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					pageWithResources(2, 4, "/Font << /F1 5 0 R >>"),
					pdfStream("", "BT /F1 12 Tf ("+strings.Repeat("A\x80\x01\x80\x02B", 6)+") Tj ET"),
					toUnicodeFont(6),
					pdfStream("", bfchars("<00> <7F>\n<8000> <FFFF>",
						[2]string{"41", "A"}, [2]string{"42", "B"}, [2]string{"8001", "C"}, [2]string{"8002", "D"})),
				)
			},
			want: "ACDBACDBACDBACDBACDBACDB",
		},
		{
			name: "compressed content",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					textPage(2, 4),
					flateStream(t, "BT (Compressed text) Tj ET"),
				)
			},
			want: "Compressed text",
		},
		{
			name: "page that is its own parent",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					textPage(3, 4),
					pdfStream("", "BT /F1 12 Tf (Hello) Tj ET"),
				)
			},
			want: "Hello",
		},
		{
			name: "page tree that lists itself as a kid",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [2 0 R] /Count 2 >>",
				)
			},
			wantErrIn: "damaged page tree",
		},
		{
			name: "page tree that lists itself as a kid many times",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids ["+refs(2, 500)+"] /Count 500 >>",
				)
			},
			wantErrIn: "damaged page tree",
		},
		{
			name:      "page tree nested too deeply",
			data:      func(*testing.T) []byte { return nestedPagesPDF(maxPDFPageTreeDepth + 1) },
			wantErrIn: "damaged page tree",
		},
		{
			name: "page tree nested as deeply as allowed",
			data: func(*testing.T) []byte { return nestedPagesPDF(maxPDFPageTreeDepth) },
			want: "Deep page",
		},
		{
			name: "too many pages",
			data: func(*testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", refs(3, maxPDFPageTreeNodes), maxPDFPageTreeNodes),
					textPage(2, 4),
					pdfStream("", "BT (Page) Tj ET"),
				)
			},
			wantErrIn: "too many pages",
		},
		{
			name: "small compressed page that expands to a huge text operand",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					textPage(2, 4),
					flateStream(t, "BT ("+strings.Repeat("A", 16<<20)+") Tj ET"),
				)
			},
			wantErrIn: `page 1 of "doc.pdf" is too complex to read`,
		},
		{
			name: "pages that together expand to too much content",
			data: func(t *testing.T) []byte {
				const pages = maxPDFStreamBytes/(maxPDFPageStreamBytes-1) + 1
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", refs(3, pages), pages),
					"<< /Type /Page /Parent 2 0 R /Contents [4 0 R 4 0 R 4 0 R 4 0 R] >>",
					flateStream(t, strings.Repeat("%", maxPDFPageStreamBytes/4-8)),
				)
			},
			wantErrIn: `the pages of "doc.pdf" are too complex to read`,
		},
		{
			name: "font character map that expands every character",
			data: func(t *testing.T) []byte {
				cmap := "1 begincodespacerange <00> <FF> endcodespacerange\n" +
					"1 beginbfchar <01> <" + strings.Repeat("0041", 200000) + "> endbfchar"
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					"<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
					flateStream(t, "BT /F1 12 Tf ("+strings.Repeat("\x01", 100000)+") Tj ET"),
					"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R >>",
					flateStream(t, cmap),
				)
			},
			wantErrIn: "more than 100000 characters",
		},
		{
			name: "many text operators over the text budget",
			data: func(t *testing.T) []byte {
				return assemblePDF(
					"<< /Type /Catalog /Pages 2 0 R >>",
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					textPage(2, 4),
					flateStream(t, strings.Repeat("BT (word) Tj ET\n", MaxTotalTextRunes)),
				)
			},
			wantErrIn: "more than 100000 characters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.data(t)

			got, err := extractWithin(t, 10*time.Second, data)

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

// extractWithin extracts the text of a PDF and fails the test if that takes
// longer than limit.
func extractWithin(t *testing.T, limit time.Duration, data []byte) (string, error) {
	t.Helper()
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := Extract(t.Context(), "doc.pdf", MimeTypePDF, data)
		done <- result{text, err}
	}()
	select {
	case res := <-done:
		return res.text, res.err
	case <-time.After(limit):
		require.FailNow(t, "reading the PDF did not finish in time", "limit %v", limit)
		return "", nil
	}
}

// usePDFLimits replaces the PDF slots and timeout for the duration of a test.
func usePDFLimits(t *testing.T, slots int, timeout time.Duration) {
	oldSlots, oldTimeout := pdfSlots, pdfExtractTimeout
	pdfSlots, pdfExtractTimeout = make(chan struct{}, slots), timeout
	t.Cleanup(func() { pdfSlots, pdfExtractTimeout = oldSlots, oldTimeout })
}

// pdfWithCyclicXrefChain returns a PDF whose cross-reference section names
// itself as its previous section, which the PDF library follows forever.
func pdfWithCyclicXrefChain() []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%" + strings.Repeat("-", 100) + "\n")
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 /Root 1 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n", xref, xref)
	return b.Bytes()
}

// pdfWithHugeXrefStream returns a PDF whose cross-reference stream declares
// two billion entries, which the PDF library allocates before reading any.
func pdfWithHugeXrefStream() []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.5\n")
	xref := b.Len()
	fmt.Fprintf(&b, "1 0 obj\n<< /Type /XRef /Size 2000000000 /W [1 1 1] /Root 1 0 R /Length 0 >>\nstream\n\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}

func TestExtractPDFContainsLibraryHazards(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantErrIn string
	}{
		{name: "cross-reference sections that loop", data: pdfWithCyclicXrefChain(), wantErrIn: "took too long"},
		{name: "cross-reference stream declaring billions of entries", data: pdfWithHugeXrefStream(), wantErrIn: "could not read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usePDFLimits(t, 2, time.Second)

			_, err := extractWithin(t, 10*time.Second, tt.data)
			require.ErrorIs(t, err, ErrInvalidDocument)
			assert.Contains(t, err.Error(), tt.wantErrIn)

			// The slot is free again and the next PDF is read normally.
			assert.Empty(t, pdfSlots)
			pdfExtractTimeout = 30 * time.Second
			text, err := extractWithin(t, 10*time.Second, assemblePDF(
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				textPage(2, 4),
				pdfStream("", "BT (Still works) Tj ET"),
			))
			require.NoError(t, err)
			assert.Equal(t, "Still works", text)
		})
	}
}

func TestExtractPDFStopsWhenContextIsDone(t *testing.T) {
	tests := []struct {
		name      string
		busySlots int
	}{
		{name: "while waiting for a slot", busySlots: 1},
		{name: "while reading", busySlots: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usePDFLimits(t, 1, 10*time.Second)
			for range tt.busySlots {
				pdfSlots <- struct{}{}
			}
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(200*time.Millisecond, cancel)

			_, err := Extract(ctx, "doc.pdf", MimeTypePDF, pdfWithCyclicXrefChain())
			require.ErrorIs(t, err, context.Canceled)
			assert.False(t, errors.Is(err, ErrInvalidDocument))
		})
	}
}

func TestExtractPDFReportsBusyServer(t *testing.T) {
	usePDFLimits(t, 1, 100*time.Millisecond)
	pdfSlots <- struct{}{}

	_, err := Extract(t.Context(), "doc.pdf", MimeTypePDF, pdfWithCyclicXrefChain())
	require.ErrorIs(t, err, ErrInvalidDocument)
	assert.Contains(t, err.Error(), "busy")
}
