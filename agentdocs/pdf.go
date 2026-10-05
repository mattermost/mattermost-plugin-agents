// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Limits on the work spent reading one PDF. The PDF library has no limits of
// its own, so a small crafted file could otherwise loop forever or expand to
// gigabytes of text.
const (
	// maxConcurrentPDFExtractions caps the PDFs read at the same time.
	maxConcurrentPDFExtractions = 2
	// maxPDFPageTreeDepth caps the nesting of the page tree.
	maxPDFPageTreeDepth = 32
	// maxPDFPageTreeNodes caps the nodes (pages and page groups) in the page tree.
	maxPDFPageTreeNodes = 10000
	// maxPDFPageStreams caps the content streams of one page.
	maxPDFPageStreams = 1000
	// maxPDFPageStreamBytes caps the decompressed content and font character
	// maps of one page.
	maxPDFPageStreamBytes = 4 << 20
	// maxPDFStreamBytes caps the decompressed content and font character maps
	// of all pages.
	maxPDFStreamBytes = 32 << 20
	// pdfDecodeChunkBytes is how much of a text operand is decoded at a time.
	// A font character map can map one code to a long string, so decoding a
	// whole operand at once has no useful memory bound. 4 bytes keeps 1, 2
	// and 4-byte character codes whole.
	pdfDecodeChunkBytes = 4
)

var (
	// pdfExtractTimeout bounds both the wait for a free PDF slot and the
	// time spent reading one PDF.
	pdfExtractTimeout = 15 * time.Second
	// pdfSlots holds one token per PDF being read.
	pdfSlots = make(chan struct{}, maxConcurrentPDFExtractions)
)

type pdfResult struct {
	text string
	err  error
}

// extractPDF reads the text of every page of a PDF.
func extractPDF(ctx context.Context, name string, data []byte) (string, error) {
	if !bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-")) {
		return "", invalidf("%q is not a PDF file", name)
	}
	return runPDFExtraction(ctx, name, func(stop <-chan struct{}) (string, error) {
		return readPDFText(name, data, stop)
	})
}

// runPDFExtraction runs extract on its own goroutine while holding a slot of
// pdfSlots, and gives up on it after pdfExtractTimeout or when ctx is done;
// stop is then closed so extract can return at its next check.
//
// An abandoned goroutine cannot be killed: the PDF library has no
// cancellation and some malformed files make it loop forever without
// reaching a check (for example a cyclic cross-reference /Prev chain). The
// goroutine releases its slot only when it actually returns, so pdfSlots
// bounds the CPU spent on PDFs even when parses get stuck; once every slot is
// stuck, new PDFs fail with a "busy" error instead of piling up.
func runPDFExtraction(ctx context.Context, name string, extract func(stop <-chan struct{}) (string, error)) (string, error) {
	slots, timeout := pdfSlots, pdfExtractTimeout

	wait := time.NewTimer(timeout)
	defer wait.Stop()
	select {
	case slots <- struct{}{}:
	case <-wait.C:
		return "", invalidf("the server is busy reading other PDF documents and could not read %q; try again in a moment", name)
	case <-ctx.Done():
		return "", fmt.Errorf("reading %q was canceled: %w", name, context.Cause(ctx))
	}

	stop := make(chan struct{})
	done := make(chan pdfResult, 1)
	go func() {
		defer func() { <-slots }()
		text, err := extract(stop)
		done <- pdfResult{text, err}
	}()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	select {
	case res := <-done:
		return res.text, res.err
	case <-deadline.C:
		close(stop)
		return "", invalidf("reading %q took too long; the PDF may be damaged or too complex", name)
	case <-ctx.Done():
		close(stop)
		return "", fmt.Errorf("reading %q was canceled: %w", name, context.Cause(ctx))
	}
}

// pdfAbort is panicked to unwind out of the PDF library's callbacks with err.
type pdfAbort struct{ err error }

// pdfTextReader accumulates the text of a PDF's pages within the limits.
type pdfTextReader struct {
	name        string
	stop        <-chan struct{}
	page        int
	pageBytes   int64
	totalBytes  int64
	runes       int
	text        strings.Builder
	treeVisited int
}

// readPDFText walks the page tree from the document catalog itself rather
// than with the library's page lookup, which follows the tree without any
// cycle or depth limit, and checks every stream's decompressed size before
// interpreting it. The library panics on malformed input, which is reported
// as an invalid document.
func readPDFText(name string, data []byte, stop <-chan struct{}) (text string, err error) {
	x := &pdfTextReader{name: name, stop: stop}
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		text = ""
		abort, aborted := r.(pdfAbort)
		switch {
		case aborted:
			err = abort.err
		case x.page > 0:
			err = invalidf("could not read page %d of %q; the PDF may be damaged", x.page, name)
		default:
			err = invalidf("could not read %q; the PDF may be damaged", name)
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", invalidf("could not read %q; the PDF may be damaged or encrypted", name)
	}
	x.walkPageTree(reader.Trailer().Key("Root").Key("Pages"), 0)
	return collapseBlankLines(normalizeText(strings.ToValidUTF8(x.text.String(), ""))), nil
}

func (x *pdfTextReader) abort(err error) {
	panic(pdfAbort{err})
}

// checkStop aborts once runPDFExtraction has given up on this read; the
// error is never seen by anyone.
func (x *pdfTextReader) checkStop() {
	select {
	case <-x.stop:
		x.abort(invalidf("reading %q was abandoned", x.name))
	default:
	}
}

// walkPageTree reads the pages under node in document order. The library
// does not expose object identities, so cycles are caught by capping the
// depth and the number of nodes visited.
func (x *pdfTextReader) walkPageTree(node pdf.Value, depth int) {
	x.checkStop()
	x.treeVisited++
	if x.treeVisited > maxPDFPageTreeNodes || depth > maxPDFPageTreeDepth {
		x.abort(invalidf("%q has too many pages or a damaged page tree", x.name))
	}
	switch node.Key("Type").Name() {
	case "Page":
		x.readPage(node)
	case "Pages":
		kids := node.Key("Kids")
		if kids.Len() > maxPDFPageTreeNodes-x.treeVisited {
			x.abort(invalidf("%q has too many pages or a damaged page tree", x.name))
		}
		for i := range kids.Len() {
			x.walkPageTree(kids.Index(i), depth+1)
		}
	}
}

func (x *pdfTextReader) readPage(page pdf.Value) {
	x.page++
	x.pageBytes = 0

	contents := page.Key("Contents")
	switch contents.Kind() {
	case pdf.Stream:
		x.chargeStream(contents)
	case pdf.Array:
		if contents.Len() > maxPDFPageStreams {
			x.abort(invalidf("page %d of %q is too complex to read", x.page, x.name))
		}
		for i := range contents.Len() {
			x.chargeStream(contents.Index(i))
		}
	default:
		return
	}

	fontDict := x.inheritedResources(page).Key("Font")
	fonts := make(map[string]*pdf.Font)
	var enc pdf.TextEncoding
	pdf.Interpret(contents, func(stk *pdf.Stack, op string) {
		x.checkStop()
		args := make([]pdf.Value, stk.Len())
		for i := len(args) - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		switch op {
		case "BT", "T*":
			x.write("\n")
		case "Tf":
			if len(args) != 2 {
				return
			}
			enc = x.fontEncoding(fontDict, fonts, args[0].Name())
		case "Tj":
			if len(args) == 1 {
				x.showText(enc, args[0].RawString())
			}
		case "'", "\"":
			if len(args) > 0 {
				x.write("\n")
				x.showText(enc, args[len(args)-1].RawString())
			}
		case "TJ":
			if len(args) != 1 {
				return
			}
			for i := range args[0].Len() {
				if s := args[0].Index(i); s.Kind() == pdf.String {
					x.showText(enc, s.RawString())
				}
			}
		}
	})
	x.write("\n")
}

// inheritedResources returns the /Resources of page or of its nearest
// ancestor that has them, following at most maxPDFPageTreeDepth parents.
func (x *pdfTextReader) inheritedResources(page pdf.Value) pdf.Value {
	v := page
	for range maxPDFPageTreeDepth + 1 {
		if v.IsNull() {
			break
		}
		if r := v.Key("Resources"); !r.IsNull() {
			return r
		}
		v = v.Key("Parent")
	}
	return pdf.Value{}
}

// fontEncoding returns the encoding of the page font called name, charging
// its character map to the page's limits the first time the font is used.
func (x *pdfTextReader) fontEncoding(fontDict pdf.Value, fonts map[string]*pdf.Font, name string) pdf.TextEncoding {
	font, ok := fonts[name]
	if !ok {
		v := fontDict.Key(name)
		if v.IsNull() {
			return nil
		}
		if toUnicode := v.Key("ToUnicode"); toUnicode.Kind() == pdf.Stream {
			x.chargeStream(toUnicode)
		}
		font = &pdf.Font{V: v}
		fonts[name] = font
	}
	return font.Encoder()
}

// chargeStream decompresses stream to count its size against the page and
// document limits, so the library never interprets an oversized stream.
func (x *pdfTextReader) chargeStream(stream pdf.Value) {
	x.checkStop()
	limit := min(maxPDFPageStreamBytes-x.pageBytes, maxPDFStreamBytes-x.totalBytes)
	n, err := io.Copy(io.Discard, io.LimitReader(stream.Reader(), limit+1))
	if err != nil {
		x.abort(invalidf("could not read page %d of %q; the PDF may be damaged", x.page, x.name))
	}
	x.pageBytes += n
	x.totalBytes += n
	if x.pageBytes > maxPDFPageStreamBytes {
		x.abort(invalidf("page %d of %q is too complex to read", x.page, x.name))
	}
	if x.totalBytes > maxPDFStreamBytes {
		x.abort(invalidf("the pages of %q are too complex to read", x.name))
	}
}

func (x *pdfTextReader) showText(enc pdf.TextEncoding, raw string) {
	if enc == nil {
		x.write(raw)
		return
	}
	for len(raw) > 0 {
		n := min(len(raw), pdfDecodeChunkBytes)
		x.write(enc.Decode(raw[:n]))
		raw = raw[n:]
	}
}

// write appends s to the text, aborting once the text is over the budget.
func (x *pdfTextReader) write(s string) {
	x.runes += utf8.RuneCountInString(s)
	if x.runes > MaxTotalTextRunes {
		x.abort(tooMuchTextError(x.name))
	}
	x.text.WriteString(s)
}
