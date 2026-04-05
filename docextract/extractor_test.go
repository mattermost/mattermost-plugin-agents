// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package docextract

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupportedType(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		expected bool
	}{
		{name: "PDF file", fileName: "document.pdf", expected: true},
		{name: "DOCX file", fileName: "document.docx", expected: true},
		{name: "XLSX file", fileName: "spreadsheet.xlsx", expected: true},
		{name: "PDF uppercase", fileName: "DOCUMENT.PDF", expected: true},
		{name: "DOCX mixed case", fileName: "Document.Docx", expected: true},
		{name: "TXT file", fileName: "notes.txt", expected: false},
		{name: "PNG file", fileName: "image.png", expected: false},
		{name: "No extension", fileName: "document", expected: false},
		{name: "Empty string", fileName: "", expected: false},
		{name: "DOC file", fileName: "old.doc", expected: false},
		{name: "XLS file", fileName: "old.xls", expected: false},
		{name: "CSV file", fileName: "data.csv", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SupportedType(tt.fileName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFileTypeFromName(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		expected string
	}{
		{name: "PDF", fileName: "doc.pdf", expected: "pdf"},
		{name: "DOCX", fileName: "doc.docx", expected: "docx"},
		{name: "XLSX", fileName: "data.xlsx", expected: "xlsx"},
		{name: "Unknown", fileName: "file.txt", expected: ""},
		{name: "Empty", fileName: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FileTypeFromName(tt.fileName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractUnsupportedType(t *testing.T) {
	ext := New()
	_, err := ext.Extract(bytes.NewReader([]byte("test")), "file.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported file type")
}

func TestExtractEmptyReader(t *testing.T) {
	ext := New()

	// An empty PDF will fail to parse
	_, err := ext.Extract(bytes.NewReader([]byte{}), "empty.pdf")
	require.Error(t, err)
}

// TestExtractDOCX tests extraction from a real DOCX file
// DOCX files are ZIP archives containing XML, so we create a minimal one
func TestExtractDOCX(t *testing.T) {
	// Create a minimal DOCX file (ZIP archive with XML content)
	docxPath := createTestDOCX(t, "Hello World. This is a test document with some content for extraction testing.")
	defer os.Remove(docxPath)

	ext := New()
	f, err := os.Open(docxPath)
	require.NoError(t, err)
	defer f.Close()

	doc, err := ext.Extract(f, filepath.Base(docxPath))
	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, "docx", doc.FileType)
	require.NotEmpty(t, doc.Pages, "should extract at least one page from DOCX")
	assert.Equal(t, 1, doc.Pages[0].PageNum)

	// Check that extracted content contains our text
	allContent := ""
	for _, page := range doc.Pages {
		allContent += page.Content
	}
	assert.Contains(t, allContent, "Hello World")
	assert.Contains(t, allContent, "test document")
}

// createTestDOCX creates a minimal DOCX file for testing
func createTestDOCX(t *testing.T, content string) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "test-*.docx")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()

	// DOCX is a ZIP file with specific XML structure
	// We'll create a minimal valid one using archive/zip
	var buf bytes.Buffer
	writer := newZipWriter(&buf)

	// [Content_Types].xml
	writer.addFile("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`)

	// _rels/.rels
	writer.addFile("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`)

	// word/_rels/document.xml.rels
	writer.addFile("word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
</Relationships>`)

	// word/document.xml
	writer.addFile("word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:wpc="http://schemas.microsoft.com/office/word/2010/wordprocessingCanvas"
            xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"
            xmlns:o="urn:schemas-microsoft-com:office:office"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"
            xmlns:v="urn:schemas-microsoft-com:vml"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:w10="urn:schemas-microsoft-com:office:word"
            xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:wne="http://schemas.microsoft.com/office/word/2006/wordml">
  <w:body>
    <w:p>
      <w:r>
        <w:t>`+content+`</w:t>
      </w:r>
    </w:p>
  </w:body>
</w:document>`)

	err = writer.close()
	require.NoError(t, err)

	_, err = tmpFile.Write(buf.Bytes())
	require.NoError(t, err)
	tmpFile.Close()

	return tmpPath
}

// TestExtractXLSX tests extraction from a real XLSX file
func TestExtractXLSX(t *testing.T) {
	xlsxPath := createTestXLSX(t)
	defer os.Remove(xlsxPath)

	ext := New()
	f, err := os.Open(xlsxPath)
	require.NoError(t, err)
	defer f.Close()

	doc, err := ext.Extract(f, filepath.Base(xlsxPath))
	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, "xlsx", doc.FileType)
	require.NotEmpty(t, doc.Pages, "should extract at least one sheet from XLSX")

	// Check that extracted content contains our data
	allContent := ""
	for _, page := range doc.Pages {
		allContent += page.Content
	}
	assert.Contains(t, allContent, "Revenue")
	assert.Contains(t, allContent, "1000")
}

// createTestXLSX creates a minimal XLSX file for testing
func createTestXLSX(t *testing.T) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "test-*.xlsx")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()

	var buf bytes.Buffer
	writer := newZipWriter(&buf)

	// [Content_Types].xml
	writer.addFile("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
</Types>`)

	// _rels/.rels
	writer.addFile("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`)

	// xl/_rels/workbook.xml.rels
	writer.addFile("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>
</Relationships>`)

	// xl/workbook.xml
	writer.addFile("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"
          xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Financial Data" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>`)

	// xl/sharedStrings.xml
	writer.addFile("xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="4" uniqueCount="4">
  <si><t>Category</t></si>
  <si><t>Revenue</t></si>
  <si><t>Product A</t></si>
  <si><t>Product B</t></si>
</sst>`)

	// xl/worksheets/sheet1.xml
	writer.addFile("xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"
           xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
    </row>
    <row r="2">
      <c r="A2" t="s"><v>2</v></c>
      <c r="B2"><v>1000</v></c>
    </row>
    <row r="3">
      <c r="A3" t="s"><v>3</v></c>
      <c r="B3"><v>2000</v></c>
    </row>
  </sheetData>
</worksheet>`)

	err = writer.close()
	require.NoError(t, err)

	_, err = tmpFile.Write(buf.Bytes())
	require.NoError(t, err)
	tmpFile.Close()

	return tmpPath
}

// TestExtractedDocumentFields verifies the metadata is set correctly
func TestExtractedDocumentFields(t *testing.T) {
	docxPath := createTestDOCX(t, "Test content")
	defer os.Remove(docxPath)

	ext := New()
	f, err := os.Open(docxPath)
	require.NoError(t, err)
	defer f.Close()

	doc, err := ext.Extract(f, "my-policy.docx")
	require.NoError(t, err)

	assert.Equal(t, "my-policy.docx", doc.FileName)
	assert.Equal(t, "docx", doc.FileType)
}

// TestExtractDOCXWithMultipleParagraphs tests multi-paragraph DOCX extraction
func TestExtractDOCXWithMultipleParagraphs(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-*.docx")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	var buf bytes.Buffer
	writer := newZipWriter(&buf)

	writer.addFile("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`)

	writer.addFile("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`)

	writer.addFile("word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
</Relationships>`)

	writer.addFile("word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p><w:r><w:t>Section 1: Data Sharing Policy</w:t></w:r></w:p>
    <w:p><w:r><w:t>All data sharing must comply with local regulations.</w:t></w:r></w:p>
    <w:p><w:r><w:t>Section 2: Partner Restrictions</w:t></w:r></w:p>
    <w:p><w:r><w:t>Partner nations may impose additional restrictions on communications systems.</w:t></w:r></w:p>
  </w:body>
</w:document>`)

	err = writer.close()
	require.NoError(t, err)
	_, err = tmpFile.Write(buf.Bytes())
	require.NoError(t, err)
	tmpFile.Close()

	ext := New()
	f, err := os.Open(tmpPath)
	require.NoError(t, err)
	defer f.Close()

	doc, err := ext.Extract(f, "policy.docx")
	require.NoError(t, err)
	require.NotEmpty(t, doc.Pages)

	allContent := strings.Join(func() []string {
		var s []string
		for _, p := range doc.Pages {
			s = append(s, p.Content)
		}
		return s
	}(), " ")

	assert.Contains(t, allContent, "Data Sharing Policy")
	assert.Contains(t, allContent, "Partner Restrictions")
	assert.Contains(t, allContent, "communications systems")
}

// zipWriter is a simple helper for creating ZIP files in tests
type zipWriter struct {
	buf     *bytes.Buffer
	zipW    *zipArchiveWriter
	entries []zipEntry
}

type zipEntry struct {
	name    string
	content string
}

type zipArchiveWriter struct {
	w *bytes.Buffer
}

func newZipWriter(buf *bytes.Buffer) *zipWriter {
	return &zipWriter{buf: buf}
}

func (w *zipWriter) addFile(name, content string) {
	w.entries = append(w.entries, zipEntry{name: name, content: content})
}

func (w *zipWriter) close() error {
	// Use archive/zip to actually write the ZIP
	zw := newArchiveZipWriter(w.buf)
	for _, entry := range w.entries {
		if err := zw.addFile(entry.name, entry.content); err != nil {
			return err
		}
	}
	return zw.close()
}
