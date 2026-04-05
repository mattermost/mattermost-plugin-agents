// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/docextract"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// DefaultMaxFileSizeMB is the default max file size for document indexing
	DefaultMaxFileSizeMB = 50
)

// IndexFile indexes a file attachment from a post
func (s *Indexer) IndexFile(ctx context.Context, fileInfo *model.FileInfo, post *model.Post, channel *model.Channel) error {
	if !s.shouldIndexFile(fileInfo) {
		return nil
	}

	if s.getSearch == nil {
		return nil
	}
	search := s.getSearch()
	if search == nil {
		return nil
	}

	// Check max file size
	maxSizeMB := DefaultMaxFileSizeMB
	if s.configGetter != nil {
		cfg := s.configGetter()
		if cfg.MaxFileSizeMB > 0 {
			maxSizeMB = cfg.MaxFileSizeMB
		}
		if !cfg.EnableDocumentIndexing {
			return nil
		}
	}
	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024
	if fileInfo.Size > maxSizeBytes {
		s.pluginAPI.LogWarn("File too large for indexing",
			"fileID", fileInfo.Id,
			"size", fileInfo.Size,
			"maxSize", maxSizeBytes)
		return nil
	}

	// Download file content
	fileReader, err := s.pluginAPI.GetFile(fileInfo.Id)
	if err != nil {
		return fmt.Errorf("failed to get file content: %w", err)
	}
	defer fileReader.Close()

	// Extract text from document
	extractor := docextract.New()
	doc, err := extractor.Extract(fileReader, fileInfo.Name)
	if err != nil {
		return fmt.Errorf("failed to extract text from %s: %w", fileInfo.Name, err)
	}

	if len(doc.Pages) == 0 {
		return nil // No text content extracted
	}

	// Create FileDocument entries for each page
	var fileDocs []embeddings.FileDocument
	for _, page := range doc.Pages {
		fileDocs = append(fileDocs, embeddings.FileDocument{
			FileID:    fileInfo.Id,
			PostID:    post.Id,
			FileName:  fileInfo.Name,
			FileType:  doc.FileType,
			CreateAt:  post.CreateAt,
			TeamID:    channel.TeamId,
			ChannelID: post.ChannelId,
			UserID:    post.UserId,
			Content:   page.Content,
			PageNum:   page.PageNum,
		})
	}

	return search.StoreFiles(ctx, fileDocs)
}

// DeleteFilesByPost deletes all file embeddings associated with a post
func (s *Indexer) DeleteFilesByPost(ctx context.Context, postID string, fileIDs []string) error {
	if s.getSearch == nil {
		return nil
	}
	search := s.getSearch()
	if search == nil {
		return nil
	}

	if len(fileIDs) == 0 {
		return nil
	}

	return search.DeleteFiles(ctx, fileIDs)
}

// shouldIndexFile returns whether a file should be indexed based on its type
func (s *Indexer) shouldIndexFile(fileInfo *model.FileInfo) bool {
	if fileInfo == nil {
		return false
	}

	return docextract.SupportedType(fileInfo.Name)
}

// indexFilesFromPost indexes all supported file attachments from a post
func (s *Indexer) indexFilesFromPost(ctx context.Context, post *model.Post, channel *model.Channel) {
	if post == nil || len(post.FileIds) == 0 {
		return
	}

	for _, fileID := range post.FileIds {
		fileInfo, err := s.pluginAPI.GetFileInfo(fileID)
		if err != nil {
			s.pluginAPI.LogWarn("Failed to get file info for indexing",
				"error", err, "fileID", fileID)
			continue
		}

		if err := s.IndexFile(ctx, fileInfo, post, channel); err != nil {
			s.pluginAPI.LogWarn("Failed to index file",
				"error", err, "fileID", fileID, "fileName", fileInfo.Name)
		}
	}
}

// reindexFiles processes file attachments in batches for reindexing
func (s *Indexer) reindexFiles(ctx context.Context, search embeddings.EmbeddingSearch, jobStatus *JobStatus, cutoffTimestamp int64) error {
	// Clear existing file embeddings
	if err := search.ClearFiles(ctx); err != nil {
		return fmt.Errorf("failed to clear file embeddings: %w", err)
	}

	// Query files with supported extensions that are attached to posts
	var lastCreateAt int64
	var lastID string
	extractor := docextract.New()

	for {
		// Check if job was canceled
		var currentStatus JobStatus
		if err := s.pluginAPI.KVGet(ReindexJobKey, &currentStatus); err == nil {
			if currentStatus.Status == JobStatusCanceled {
				return fmt.Errorf("job canceled")
			}
		}

		// Query FileInfo records for supported document types
		type FileRecord struct {
			FileID    string `db:"id"`
			PostID    string `db:"postid"`
			Name      string `db:"name"`
			Size      int64  `db:"size"`
			CreateAt  int64  `db:"createat"`
			ChannelID string `db:"channelid"`
			TeamID    string `db:"teamid"`
			UserID    string `db:"userid"`
		}

		var files []FileRecord
		query := `SELECT
			fi.Id as id,
			fi.PostId as postid,
			fi.Name as name,
			fi.Size as size,
			fi.CreateAt as createat,
			p.ChannelId as channelid,
			COALESCE(c.TeamId, '') as teamid,
			p.UserId as userid
		FROM FileInfo fi
		JOIN Posts p ON fi.PostId = p.Id
		LEFT JOIN Channels c ON p.ChannelId = c.Id
		WHERE fi.DeleteAt = 0
			AND p.DeleteAt = 0
			AND (fi.Extension = 'pdf' OR fi.Extension = 'docx' OR fi.Extension = 'xlsx')
			AND (fi.CreateAt, fi.Id) > ($1, $2)
			AND fi.CreateAt <= $3
		ORDER BY fi.CreateAt ASC, fi.Id ASC
		LIMIT $4`

		err := s.db.SelectContext(ctx, &files, query, lastCreateAt, lastID, cutoffTimestamp, defaultBatchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch files for reindexing: %w", err)
		}

		if len(files) == 0 {
			break
		}

		// Process each file
		for _, file := range files {
			// Check max file size
			maxSizeMB := DefaultMaxFileSizeMB
			if s.configGetter != nil {
				cfg := s.configGetter()
				if cfg.MaxFileSizeMB > 0 {
					maxSizeMB = cfg.MaxFileSizeMB
				}
			}
			maxSizeBytes := int64(maxSizeMB) * 1024 * 1024
			if file.Size > maxSizeBytes {
				continue
			}

			// Download file content
			fileReader, err := s.pluginAPI.GetFile(file.FileID)
			if err != nil {
				s.pluginAPI.LogWarn("Failed to get file for reindexing",
					"error", err, "fileID", file.FileID)
				continue
			}

			doc, err := extractor.Extract(fileReader, file.Name)
			fileReader.Close()
			if err != nil {
				s.pluginAPI.LogWarn("Failed to extract file for reindexing",
					"error", err, "fileID", file.FileID, "fileName", file.Name)
				continue
			}

			if len(doc.Pages) == 0 {
				continue
			}

			var fileDocs []embeddings.FileDocument
			for _, page := range doc.Pages {
				fileDocs = append(fileDocs, embeddings.FileDocument{
					FileID:    file.FileID,
					PostID:    file.PostID,
					FileName:  file.Name,
					FileType:  doc.FileType,
					CreateAt:  file.CreateAt,
					TeamID:    file.TeamID,
					ChannelID: file.ChannelID,
					UserID:    file.UserID,
					Content:   page.Content,
					PageNum:   page.PageNum,
				})
			}

			if err := search.StoreFiles(ctx, fileDocs); err != nil {
				s.pluginAPI.LogWarn("Failed to store file documents during reindex",
					"error", err, "fileID", file.FileID)
				continue
			}
		}

		// Update cursor
		lastFile := files[len(files)-1]
		lastCreateAt = lastFile.CreateAt
		lastID = lastFile.FileID

		// Update heartbeat
		jobStatus.LastUpdatedAt = timeNow()
		s.saveJobStatus(jobStatus)
	}

	return nil
}

// Ensure we use a var for time.Now so tests could override if needed
var timeNow = func() time.Time { return time.Now() }
