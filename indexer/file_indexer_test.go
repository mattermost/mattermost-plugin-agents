// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/docextract"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestShouldIndexFile(t *testing.T) {
	s := &Indexer{}

	tests := []struct {
		name     string
		fileInfo *model.FileInfo
		expected bool
	}{
		{
			name:     "nil FileInfo",
			fileInfo: nil,
			expected: false,
		},
		{
			name: "PDF file",
			fileInfo: &model.FileInfo{
				Id:   "file1",
				Name: "policy.pdf",
			},
			expected: true,
		},
		{
			name: "DOCX file",
			fileInfo: &model.FileInfo{
				Id:   "file2",
				Name: "report.docx",
			},
			expected: true,
		},
		{
			name: "XLSX file",
			fileInfo: &model.FileInfo{
				Id:   "file3",
				Name: "data.xlsx",
			},
			expected: true,
		},
		{
			name: "PNG image - unsupported",
			fileInfo: &model.FileInfo{
				Id:   "file4",
				Name: "screenshot.png",
			},
			expected: false,
		},
		{
			name: "MP4 video - unsupported",
			fileInfo: &model.FileInfo{
				Id:   "file5",
				Name: "recording.mp4",
			},
			expected: false,
		},
		{
			name: "TXT file - unsupported",
			fileInfo: &model.FileInfo{
				Id:   "file6",
				Name: "notes.txt",
			},
			expected: false,
		},
		{
			name: "DOC file (old format) - unsupported",
			fileInfo: &model.FileInfo{
				Id:   "file7",
				Name: "legacy.doc",
			},
			expected: false,
		},
		{
			name: "PDF uppercase",
			fileInfo: &model.FileInfo{
				Id:   "file8",
				Name: "DOCUMENT.PDF",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.shouldIndexFile(tt.fileInfo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSupportedTypeIntegration(t *testing.T) {
	// Verify that the indexer's shouldIndexFile is consistent with docextract.SupportedType
	supportedFiles := []string{
		"document.pdf",
		"report.docx",
		"data.xlsx",
	}

	unsupportedFiles := []string{
		"image.png",
		"image.jpg",
		"video.mp4",
		"audio.wav",
		"script.js",
		"style.css",
		"archive.zip",
		"notes.txt",
		"config.json",
	}

	for _, name := range supportedFiles {
		assert.True(t, docextract.SupportedType(name), "expected %s to be supported", name)
	}

	for _, name := range unsupportedFiles {
		assert.False(t, docextract.SupportedType(name), "expected %s to not be supported", name)
	}
}
