// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package docextract

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsawler/tabula"
)

// Supported file extensions for document extraction
var supportedExtensions = map[string]string{
	".pdf":  "pdf",
	".docx": "docx",
	".xlsx": "xlsx",
}

// ExtractedPage represents a page/section/sheet of extracted content
type ExtractedPage struct {
	PageNum int    // 1-based page number (or sheet number for XLSX)
	Content string // Extracted text content
}

// ExtractedDocument represents the full extraction result from a document
type ExtractedDocument struct {
	FileName string
	FileType string // "pdf", "docx", "xlsx"
	Pages    []ExtractedPage
}

// Extractor extracts text from document files
type Extractor struct{}

// New creates a new document Extractor
func New() *Extractor {
	return &Extractor{}
}

// SupportedType returns true if the file type is supported for extraction
func SupportedType(fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	_, ok := supportedExtensions[ext]
	return ok
}

// FileTypeFromName returns the document type string for a filename
func FileTypeFromName(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	ft, ok := supportedExtensions[ext]
	if !ok {
		return ""
	}
	return ft
}

// Extract reads file content from a reader and extracts text organized by page/sheet.
// The fileName is used to determine the file type from the extension.
func (e *Extractor) Extract(reader io.Reader, fileName string) (*ExtractedDocument, error) {
	if !SupportedType(fileName) {
		return nil, fmt.Errorf("unsupported file type: %s", filepath.Ext(fileName))
	}

	fileType := FileTypeFromName(fileName)

	// Write content to a temporary file since tabula requires a filename
	tmpFile, err := os.CreateTemp("", "docextract-*"+filepath.Ext(fileName))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, reader); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	doc := &ExtractedDocument{
		FileName: fileName,
		FileType: fileType,
	}

	switch fileType {
	case "pdf":
		return e.extractPDF(tmpPath, doc)
	case "docx":
		return e.extractDOCX(tmpPath, doc)
	case "xlsx":
		return e.extractXLSX(tmpPath, doc)
	default:
		return nil, fmt.Errorf("unsupported file type: %s", fileType)
	}
}

// extractPDF extracts text from a PDF file, one page at a time
func (e *Extractor) extractPDF(filePath string, doc *ExtractedDocument) (*ExtractedDocument, error) {
	ext := tabula.Open(filePath)
	pageCount, err := ext.PageCount()
	if err != nil {
		// Try extracting all text as a single page
		ext2 := tabula.Open(filePath)
		text, _, textErr := ext2.ExcludeHeaders().ExcludeFooters().Text()
		if textErr != nil {
			return nil, fmt.Errorf("failed to extract PDF text: %w (page count error: %v)", textErr, err)
		}
		text = strings.TrimSpace(text)
		if text != "" {
			doc.Pages = append(doc.Pages, ExtractedPage{PageNum: 1, Content: text})
		}
		return doc, nil
	}

	if pageCount == 0 {
		return doc, nil
	}

	// Extract each page individually
	for i := 1; i <= pageCount; i++ {
		pageExt := tabula.Open(filePath)
		text, _, textErr := pageExt.Pages(i).ExcludeHeaders().ExcludeFooters().Text()
		if textErr != nil {
			continue // Skip pages that fail to extract
		}
		text = strings.TrimSpace(text)
		if text != "" {
			doc.Pages = append(doc.Pages, ExtractedPage{PageNum: i, Content: text})
		}
	}

	return doc, nil
}

// extractDOCX extracts text from a DOCX file as a single page
func (e *Extractor) extractDOCX(filePath string, doc *ExtractedDocument) (*ExtractedDocument, error) {
	ext := tabula.Open(filePath)
	text, _, err := ext.ExcludeHeaders().ExcludeFooters().Text()
	if err != nil {
		return nil, fmt.Errorf("failed to extract DOCX text: %w", err)
	}

	text = strings.TrimSpace(text)
	if text != "" {
		doc.Pages = append(doc.Pages, ExtractedPage{PageNum: 1, Content: text})
	}

	return doc, nil
}

// extractXLSX extracts text from an XLSX file, one sheet at a time
func (e *Extractor) extractXLSX(filePath string, doc *ExtractedDocument) (*ExtractedDocument, error) {
	ext := tabula.Open(filePath)
	pageCount, err := ext.PageCount()
	if err != nil {
		// Try extracting all text as a single page
		ext2 := tabula.Open(filePath)
		text, _, textErr := ext2.Text()
		if textErr != nil {
			return nil, fmt.Errorf("failed to extract XLSX text: %w (page count error: %v)", textErr, err)
		}
		text = strings.TrimSpace(text)
		if text != "" {
			doc.Pages = append(doc.Pages, ExtractedPage{PageNum: 1, Content: text})
		}
		return doc, nil
	}

	if pageCount == 0 {
		return doc, nil
	}

	// Extract each sheet individually
	for i := 1; i <= pageCount; i++ {
		sheetExt := tabula.Open(filePath)
		text, _, textErr := sheetExt.Pages(i).Text()
		if textErr != nil {
			continue // Skip sheets that fail to extract
		}
		text = strings.TrimSpace(text)
		if text != "" {
			doc.Pages = append(doc.Pages, ExtractedPage{PageNum: i, Content: text})
		}
	}

	return doc, nil
}
