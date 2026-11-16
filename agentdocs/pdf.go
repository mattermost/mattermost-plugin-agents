// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"bytes"
	"context"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Limits on the work spent reading one PDF. The PDF library has no limits of
// its own, so a small crafted file could otherwise loop forever or expand to
// gigabytes of memory. Reading happens in a worker process (see pdf_worker.go)
// that is killed on timeout and runs under a memory limit, because the library
// can loop or allocate before any of the checks below can run.
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
	// maxPDFFormDepth caps the nesting of Form XObjects.
	maxPDFFormDepth = 8
	// maxPDFForms caps the Form XObject invocations in one document.
	maxPDFForms = 1000
	// maxPDFGraphicsStateDepth caps the nesting of q ... Q.
	maxPDFGraphicsStateDepth = 256
)

var (
	// pdfExtractTimeout bounds both the wait for a free PDF slot and the
	// time spent reading one PDF.
	pdfExtractTimeout = 15 * time.Second
	// pdfSlots holds one token per PDF being read.
	pdfSlots = make(chan struct{}, maxConcurrentPDFExtractions)
)

// extractPDF reads the text of every page of a PDF in a worker process.
func extractPDF(ctx context.Context, name string, data []byte) (string, error) {
	if !bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-")) {
		return "", invalidf("%q is not a PDF file", name)
	}
	return runPDFWorker(ctx, name, data)
}

// pdfAbort is panicked to unwind out of the PDF library's callbacks with err.
type pdfAbort struct{ err error }

// pdfTextReader accumulates the text of a PDF's pages within the limits.
type pdfTextReader struct {
	name        string
	page        int
	pageBytes   int64
	totalBytes  int64
	runes       int
	text        strings.Builder
	treeVisited int
	forms       int
	baseline    float64
}

// readPDFText walks the page tree from the document catalog itself rather
// than with the library's page lookup, which follows the tree without any
// cycle or depth limit, and checks every stream's decompressed size before
// interpreting it. The library panics on malformed input, which is reported
// as an invalid document.
func readPDFText(name string, data []byte) (text string, err error) {
	x := &pdfTextReader{name: name}
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

// walkPageTree reads the pages under node in document order. The library
// does not expose object identities, so cycles are caught by capping the
// depth and the number of nodes visited.
func (x *pdfTextReader) walkPageTree(node pdf.Value, depth int) {
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

	x.interpret(contents, x.inheritedResources(page), 0)
	x.write("\n")
}

// interpret reads the text shown by a content stream, or a Form XObject's
// stream, using resources for its fonts and nested forms. Font changes made
// inside q ... Q, and by a form, do not outlive them.
func (x *pdfTextReader) interpret(contents, resources pdf.Value, depth int) {
	fontDict := resources.Key("Font")
	fonts := make(map[string]*pdfFontEncoding)
	var enc *pdfFontEncoding
	var saved []*pdfFontEncoding
	pdf.Interpret(contents, func(stk *pdf.Stack, op string) {
		args := make([]pdf.Value, stk.Len())
		for i := len(args) - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		switch op {
		case "q":
			if len(saved) < maxPDFGraphicsStateDepth {
				saved = append(saved, enc)
			}
		case "Q":
			if n := len(saved); n > 0 {
				enc, saved = saved[n-1], saved[:n-1]
			}
		case "Do":
			if len(args) == 1 {
				x.drawForm(resources.Key("XObject").Key(args[0].Name()), resources, depth)
			}
		case "BT", "T*":
			x.write("\n")
		case "Td", "TD":
			if len(args) == 2 {
				x.moveText(args[1].Float64())
			}
		case "Tm":
			if len(args) == 6 {
				x.setTextMatrix(args[5].Float64())
			}
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
}

// drawForm reads the text of a Form XObject invoked by Do. A form without
// resources of its own uses those of the content that invoked it. Every
// invocation is charged to the stream limits, and nesting and the number of
// invocations are capped, so forms that invoke themselves are rejected.
func (x *pdfTextReader) drawForm(form, parentResources pdf.Value, depth int) {
	if form.Kind() != pdf.Stream || form.Key("Subtype").Name() != "Form" {
		return
	}
	x.forms++
	if depth >= maxPDFFormDepth || x.forms > maxPDFForms {
		x.abort(invalidf("page %d of %q is too complex to read", x.page, x.name))
	}
	x.chargeStream(form)
	resources := form.Key("Resources")
	if resources.IsNull() {
		resources = parentResources
	}
	x.interpret(form, resources, depth+1)
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
func (x *pdfTextReader) fontEncoding(fontDict pdf.Value, fonts map[string]*pdfFontEncoding, name string) *pdfFontEncoding {
	if enc, ok := fonts[name]; ok {
		return enc
	}
	v := fontDict.Key(name)
	if v.IsNull() {
		return nil
	}
	toUnicode := v.Key("ToUnicode")
	if toUnicode.Kind() == pdf.Stream {
		x.chargeStream(toUnicode)
	}
	font := &pdf.Font{V: v}
	enc := &pdfFontEncoding{enc: font.Encoder()}
	if toUnicode.Kind() == pdf.Stream && usesCharacterMap(v) {
		enc.codespace = readCodespace(toUnicode)
	}
	fonts[name] = enc
	return enc
}

// chargeStream decompresses stream to count its size against the page and
// document limits, so the library never interprets an oversized stream.
func (x *pdfTextReader) chargeStream(stream pdf.Value) {
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

// moveText separates the text before and after a Td or TD move: a vertical
// move starts a new line, a horizontal one separates words.
func (x *pdfTextReader) moveText(dy float64) {
	if dy != 0 {
		x.write("\n")
		return
	}
	x.write(" ")
}

// setTextMatrix separates the text before and after a Tm, which positions
// text absolutely, by comparing the new baseline with the last one.
func (x *pdfTextReader) setTextMatrix(y float64) {
	if y != x.baseline {
		x.write("\n")
	} else {
		x.write(" ")
	}
	x.baseline = y
}

// showText decodes the text operand raw with enc, a few character codes at a
// time so the output of one decode is bounded.
func (x *pdfTextReader) showText(enc *pdfFontEncoding, raw string) {
	if enc == nil {
		x.write(raw)
		return
	}
	for raw != "" {
		n := enc.chunkLen(raw)
		x.write(enc.enc.Decode(raw[:n]))
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
