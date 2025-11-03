// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// fetchFileDataForLocal fetches file data from a file path or URL (local access only)
func fetchFileDataForLocal(filespec string, accessMode AccessMode) ([]byte, error) {
	if filespec == "" {
		return nil, fmt.Errorf("empty filespec provided")
	}

	// URLs are only allowed for local access mode
	if accessMode != AccessModeLocal {
		return nil, fmt.Errorf("URL access not supported in remote access mode, only local access allows URL access")
	}

	// Check if it's a URL
	if isURL(filespec) {
		resp, err := http.Get(filespec) // #nosec G107 - filespec is validated to be URL
		if err != nil {
			return nil, fmt.Errorf("failed to fetch file from URL: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch file: HTTP %d", resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read file data: %w", err)
		}

		return data, nil
	}

	cleanPath := filepath.Clean(filespec)
	file, err := os.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return data, nil
}

// isURL checks if a string is a URL
func isURL(filespec string) bool {
	return strings.HasPrefix(filespec, "http://") || strings.HasPrefix(filespec, "https://")
}

// extractFileNameForLocal extracts the filename from a filespec (URL or file path, local access only)
func extractFileNameForLocal(filespec string, accessMode AccessMode) string {
	if filespec == "" {
		return ""
	}

	if accessMode != AccessModeLocal {
		return "url-access-denied"
	}

	// Handle URLs - only allowed for local access
	if isURL(filespec) {
		parsedURL, err := url.Parse(filespec)
		if err != nil {
			// Fallback to simple string splitting if URL parsing fails
			parts := strings.Split(filespec, "/")
			if len(parts) > 0 {
				return parts[len(parts)-1]
			}
			return "unknown"
		}

		filename := filepath.Base(parsedURL.Path)
		if filename == "" || filename == "." || filename == "/" {
			return "unknown"
		}
		return filename
	}

	// For local file paths, extract the base name
	return filepath.Base(filespec)
}

// isValidImageFile checks if the file extension is a supported image format
func isValidImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	validExts := []string{".jpeg", ".jpg", ".png", ".gif"}
	for _, validExt := range validExts {
		if ext == validExt {
			return true
		}
	}
	return false
}

// uploadFilesForLocal uploads multiple files from URLs or file paths (local access only) and returns their file IDs
func uploadFilesForLocal(ctx context.Context, client *model.Client4, channelID string, filespecs []string, accessMode AccessMode) ([]string, error) {
	var fileIDs []string

	// Early validation - only local access can upload files
	if accessMode != AccessModeLocal {
		return nil, fmt.Errorf("file uploads not supported in remote access mode, only local access allows file operations")
	}

	for _, filespec := range filespecs {
		if filespec == "" {
			continue
		}

		fileData, err := fetchFileDataForLocal(filespec, accessMode)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch file %s: %w", filespec, err)
		}

		fileName := extractFileNameForLocal(filespec, accessMode)
		if fileName == "" || fileName == "url-access-denied" || fileName == "file-access-denied" {
			fileName = "attachment"
		}

		fileUploadResponse, _, err := client.UploadFileAsRequestBody(ctx, fileData, channelID, fileName)
		if err != nil {
			return nil, fmt.Errorf("failed to upload file %s: %w", filespec, err)
		}

		if len(fileUploadResponse.FileInfos) > 0 {
			fileIDs = append(fileIDs, fileUploadResponse.FileInfos[0].Id)
		}
	}

	return fileIDs, nil
}

// uploadFilesAndUrlsForLocal uploads files from URLs or file paths (local access only) and returns file IDs and status message
func uploadFilesAndUrlsForLocal(ctx context.Context, client *model.Client4, channelID string, attachments []string, accessMode AccessMode) ([]string, string) {
	var fileIDs []string
	var attachmentMessage string

	if len(attachments) > 0 {
		if accessMode != AccessModeLocal {
			attachmentMessage = " (file attachments not supported in remote access mode)"
			return nil, attachmentMessage
		}

		uploadedFileIDs, uploadErr := uploadFilesForLocal(ctx, client, channelID, attachments, accessMode)
		if uploadErr != nil {
			attachmentMessage = fmt.Sprintf(" (file upload failed: %v)", uploadErr)
		} else {
			fileIDs = uploadedFileIDs
			attachmentMessage = fmt.Sprintf(" (uploaded %d files)", len(fileIDs))
		}
	}

	return fileIDs, attachmentMessage
}
