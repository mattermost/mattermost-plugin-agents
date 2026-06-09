// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package files serves the text contents of Mattermost file attachments to the
// LLM on demand. It exists because the server-extracted text (FileInfo.Content,
// used for PDFs and Office documents) is tagged json:"-" and is never returned
// over the REST API, so the MCP server's user-scoped client cannot read it.
// This service reaches that content through the admin plugin API and therefore
// must enforce the requesting user's channel permissions itself.
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// DefaultReadRunes is the window size returned when a caller omits a limit.
	DefaultReadRunes = 6000
	// MaxReadRunes caps a single read so one tool call cannot re-blow the
	// context window the lazy-loading design is meant to protect.
	MaxReadRunes = 20000
	// maxDownloadBytes bounds how much of a raw text file is read into memory
	// when the server has no pre-extracted content for it.
	maxDownloadBytes = 10 * 1024 * 1024
)

// ErrForbidden is returned when the requesting user lacks permission to read the
// file's channel. Callers map it to a 403 / access-denied tool result.
var ErrForbidden = errors.New("you do not have permission to access this file")

// Content is a ranged slice of a file's text plus the metadata needed to page
// through the rest of it. Offsets are measured in runes, not bytes, so a
// multi-byte character is never split across reads.
type Content struct {
	Name       string
	MimeType   string
	TotalRunes int
	Offset     int
	Returned   int
	HasMore    bool
	HasText    bool // false means the file has no extractable text (e.g. a binary with no server-side extraction)
	Text       string
}

// Service is the shared plugin-side file reader behind both the embedded
// read_file tool and the /files/content HTTP endpoint.
type Service struct {
	mm mmapi.Client
}

// New creates a file content service backed by the given Mattermost client.
func New(mm mmapi.Client) *Service {
	return &Service{mm: mm}
}

// GetContent returns the [offset, offset+limit) rune window of a file's text for
// userID, after verifying that user may read the file's channel. Documents use
// the server-extracted text; plain text files fall back to the raw bytes.
func (s *Service) GetContent(ctx context.Context, userID, fileID string, offset, limit int) (Content, error) {
	if !model.IsValidId(fileID) {
		return Content{}, fmt.Errorf("invalid file id")
	}

	fileInfo, err := s.mm.GetFileInfo(fileID)
	if err != nil {
		return Content{}, fmt.Errorf("failed to get file info: %w", err)
	}

	// The plugin API is admin-level, so permission must be checked explicitly.
	if fileInfo.ChannelId == "" || !s.mm.HasPermissionToChannel(userID, fileInfo.ChannelId, model.PermissionReadChannel) {
		return Content{}, ErrForbidden
	}

	text := s.extractText(ctx, fileInfo)
	if text == "" {
		return Content{Name: fileInfo.Name, MimeType: fileInfo.MimeType, HasText: false}, nil
	}

	return Slice(fileInfo.Name, fileInfo.MimeType, text, offset, limit), nil
}

// Slice returns the [offset, offset+limit) rune window of text as a Content,
// applying the default and maximum read sizes. Offsets are measured in runes so
// a multibyte character is never split, and they are clamped into range.
func Slice(name, mimeType, text string, offset, limit int) Content {
	runes := []rune(text)
	total := len(runes)

	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 {
		limit = DefaultReadRunes
	}
	if limit > MaxReadRunes {
		limit = MaxReadRunes
	}
	end := offset + limit
	if end > total {
		end = total
	}

	return Content{
		Name:       name,
		MimeType:   mimeType,
		TotalRunes: total,
		Offset:     offset,
		Returned:   end - offset,
		HasMore:    end < total,
		HasText:    total > 0,
		Text:       string(runes[offset:end]),
	}
}

// extractText prefers the server-extracted content (the only source for PDFs and
// Office documents) and falls back to the raw bytes for plain text files.
func (s *Service) extractText(_ context.Context, fileInfo *model.FileInfo) string {
	if trimmed := strings.TrimSpace(fileInfo.Content); trimmed != "" {
		return trimmed
	}

	if !strings.HasPrefix(fileInfo.MimeType, "text/") {
		return ""
	}

	reader, err := s.mm.GetFile(fileInfo.Id)
	if err != nil {
		s.mm.LogError("failed to get file for read_file", "error", err, "file_id", fileInfo.Id)
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxDownloadBytes))
	if closeErr := reader.Close(); closeErr != nil {
		s.mm.LogWarn("failed to close file reader for read_file", "error", closeErr, "file_id", fileInfo.Id)
	}
	if err != nil {
		s.mm.LogError("failed to read file for read_file", "error", err, "file_id", fileInfo.Id)
		return ""
	}
	return string(body)
}
