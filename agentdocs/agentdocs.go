// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package agentdocs validates agent reference documents and extracts the
// text that is added to an agent's system prompt.
package agentdocs

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Limits for agent reference documents. The webapp mirrors these values.
const (
	// MaxDocumentBytes caps the original size of one document.
	MaxDocumentBytes = 10 << 20
	// MaxDocumentsPerAgent caps how many documents one agent can reference.
	MaxDocumentsPerAgent = 20
	// MaxTotalBytesPerAgent caps the summed original size of an agent's documents.
	MaxTotalBytesPerAgent = 25 << 20
	// MaxTotalTextRunes caps the summed extracted text of an agent's documents,
	// which is sent with every request to the agent.
	MaxTotalTextRunes = 100000
	// MaxNameRunes caps a document's display name.
	MaxNameRunes = 256
)

// Supported document MIME types.
const (
	MimeTypePDF      = "application/pdf"
	MimeTypeText     = "text/plain"
	MimeTypeMarkdown = "text/markdown"
	MimeTypeCSV      = "text/csv"
	MimeTypeJSON     = "application/json"
)

// pdfExtractTimeout bounds the time spent extracting text from one PDF.
const pdfExtractTimeout = 30 * time.Second

var mimeTypesByExtension = map[string]string{
	".pdf":      MimeTypePDF,
	".txt":      MimeTypeText,
	".md":       MimeTypeMarkdown,
	".markdown": MimeTypeMarkdown,
	".csv":      MimeTypeCSV,
	".json":     MimeTypeJSON,
}

// ErrInvalidDocument matches every error caused by the document itself
// (type, content, name or limits) rather than by the server. Those errors'
// messages are meant for the user and do not repeat this one.
var ErrInvalidDocument = errors.New("invalid reference document")

type invalidDocumentError struct{ msg string }

func (e *invalidDocumentError) Error() string { return e.msg }
func (e *invalidDocumentError) Unwrap() error { return ErrInvalidDocument }

func invalidf(format string, args ...any) error {
	return &invalidDocumentError{msg: fmt.Sprintf(format, args...)}
}

// MimeTypeForName returns the MIME type of a supported document file name,
// decided by its extension.
func MimeTypeForName(name string) (string, error) {
	mimeType, ok := mimeTypesByExtension[strings.ToLower(path.Ext(name))]
	if !ok {
		return "", invalidf("%q is not a supported document type; upload a PDF, text, Markdown, CSV or JSON file", name)
	}
	return mimeType, nil
}

// IsSupportedMimeType reports whether mimeType is a supported document type.
func IsSupportedMimeType(mimeType string) bool {
	for _, supported := range mimeTypesByExtension {
		if supported == mimeType {
			return true
		}
	}
	return false
}

// NormalizeName trims name and checks that it is a usable document name:
// 1 to MaxNameRunes characters without path separators or control characters.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", invalidf("document names cannot be empty")
	case !utf8.ValidString(name):
		return "", invalidf("document names must be valid UTF-8")
	case utf8.RuneCountInString(name) > MaxNameRunes:
		return "", invalidf("document names cannot be longer than %d characters", MaxNameRunes)
	case strings.ContainsAny(name, `/\`):
		return "", invalidf("document name %q cannot contain / or \\", name)
	case strings.ContainsFunc(name, unicode.IsControl):
		return "", invalidf("document name %q cannot contain control characters", name)
	}
	return name, nil
}

// Extract returns the normalized text of a document of the given MIME type.
// name is only used in error messages. Every error wraps ErrInvalidDocument:
// unsupported or mismatching content, no extractable text, or more than
// MaxTotalTextRunes characters of text (a document an agent could never hold).
func Extract(name, mimeType string, data []byte) (string, error) {
	var (
		text string
		err  error
	)
	switch mimeType {
	case MimeTypePDF:
		text, err = extractPDF(name, data)
	case MimeTypeText, MimeTypeMarkdown, MimeTypeCSV, MimeTypeJSON:
		text, err = extractText(name, data)
	default:
		return "", invalidf("%q is not a supported document type", name)
	}
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", invalidf("no extractable text found in %q", name)
	}
	if utf8.RuneCountInString(text) > MaxTotalTextRunes {
		return "", tooMuchTextError(name)
	}
	return text, nil
}

func tooMuchTextError(name string) error {
	return invalidf("%q has more than %d characters of extracted text, the limit for all of an agent's documents", name, MaxTotalTextRunes)
}

func extractText(name string, data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", invalidf("%q is not valid UTF-8 text", name)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", invalidf("%q is not a text file", name)
	}
	return normalizeText(string(data)), nil
}

// extractPDF reads the text of every page. The PDF library can panic on
// malformed input, which is reported as an invalid document. Extraction stops
// once the text exceeds MaxTotalTextRunes or pdfExtractTimeout elapses.
func extractPDF(name string, data []byte) (text string, err error) {
	if !bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-")) {
		return "", invalidf("%q is not a PDF file", name)
	}
	defer func() {
		if r := recover(); r != nil {
			text, err = "", invalidf("could not read %q; the PDF may be damaged", name)
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", invalidf("could not read %q; the PDF may be damaged or encrypted", name)
	}

	deadline := time.Now().Add(pdfExtractTimeout)
	var b strings.Builder
	runes := 0
	for i := 1; i <= reader.NumPage(); i++ {
		if time.Now().After(deadline) {
			return "", invalidf("reading %q took too long", name)
		}
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		pageText, pageErr := page.GetPlainText(nil)
		if pageErr != nil {
			return "", invalidf("could not read page %d of %q; the PDF may be damaged", i, name)
		}
		b.WriteString(pageText)
		b.WriteString("\n")
		runes += utf8.RuneCountInString(pageText)
		if runes > MaxTotalTextRunes {
			return "", tooMuchTextError(name)
		}
	}
	return collapseBlankLines(normalizeText(strings.ToValidUTF8(b.String(), ""))), nil
}

// normalizeText drops a byte order mark and control characters other than
// newlines and tabs, normalizes line endings to \n and trims the text.
func normalizeText(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// collapseBlankLines trims trailing whitespace from every line and keeps at
// most one blank line in a row; PDF text extraction produces many of both.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
