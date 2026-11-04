// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"runtime"
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

func TestExtractPDF(t *testing.T) {
	tests := []struct {
		name      string
		data      func(t *testing.T) []byte
		want      string
		wantErrIn string
		// maxAlloc bounds the bytes allocated while reading the document.
		maxAlloc uint64
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
			maxAlloc:  32 << 20,
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
			maxAlloc:  32 << 20,
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
			maxAlloc:  64 << 20,
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
			maxAlloc:  32 << 20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.data(t)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)

			got, err := extractWithin(t, 5*time.Second, data)

			runtime.ReadMemStats(&after)
			if tt.maxAlloc > 0 {
				assert.Less(t, after.TotalAlloc-before.TotalAlloc, tt.maxAlloc, "bytes allocated while reading")
			}
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
	done := make(chan pdfResult, 1)
	go func() {
		text, err := Extract(t.Context(), "doc.pdf", MimeTypePDF, data)
		done <- pdfResult{text, err}
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

func TestRunPDFExtractionBoundsStuckReads(t *testing.T) {
	usePDFLimits(t, 1, 100*time.Millisecond)

	release := make(chan struct{})
	stopped := make(chan struct{})
	finished := make(chan struct{})
	t.Cleanup(func() {
		close(release)
		<-finished
	})
	stuck := func(stop <-chan struct{}) (string, error) {
		defer close(finished)
		<-stop
		close(stopped)
		<-release
		return "ignored", nil
	}
	quick := func(<-chan struct{}) (string, error) { return "text", nil }

	_, err := runPDFExtraction(t.Context(), "stuck.pdf", stuck)
	require.ErrorIs(t, err, ErrInvalidDocument)
	assert.Contains(t, err.Error(), "took too long")
	select {
	case <-stopped:
	case <-time.After(time.Second):
		require.FailNow(t, "the abandoned read was not told to stop")
	}

	// The stuck read still holds the only slot.
	_, err = runPDFExtraction(t.Context(), "next.pdf", quick)
	require.ErrorIs(t, err, ErrInvalidDocument)
	assert.Contains(t, err.Error(), "busy")

	release <- struct{}{}
	<-finished
	text, err := runPDFExtraction(t.Context(), "next.pdf", quick)
	require.NoError(t, err)
	assert.Equal(t, "text", text)
}

func TestRunPDFExtractionStopsWhenContextIsDone(t *testing.T) {
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
			time.AfterFunc(50*time.Millisecond, cancel)

			_, err := runPDFExtraction(ctx, "doc.pdf", func(stop <-chan struct{}) (string, error) {
				<-stop
				return "", nil
			})
			require.ErrorIs(t, err, context.Canceled)
			assert.False(t, errors.Is(err, ErrInvalidDocument))
		})
	}
}
