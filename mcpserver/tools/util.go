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

// getDataDirectory returns the path to the MCP server data directory
func getDataDirectory() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// More explicit: expect binary in mcpserver/bin, data in mcpserver/data
	execDir := filepath.Dir(execPath)
	mcpRoot := filepath.Dir(execDir) // up one level from bin/
	dataDir := filepath.Join(mcpRoot, "data")

	// Validate the structure to catch deployment issues early
	if !strings.HasSuffix(execDir, "bin") {
		return "", fmt.Errorf("executable not in expected 'bin' directory structure")
	}

	return dataDir, nil
}

// EnsureDataDirectory creates the data directory if it doesn't exist
func EnsureDataDirectory() error {
	dataDir, err := getDataDirectory()
	if err != nil {
		return err
	}
	return os.MkdirAll(dataDir, 0700)
}

// fetchFileData fetches file data from a file path or URL and returns it as []byte
func fetchFileData(filespec string) ([]byte, error) {
	if filespec == "" {
		return nil, fmt.Errorf("empty filespec provided")
	}

	// Check if it's a URL
	if strings.HasPrefix(filespec, "http://") || strings.HasPrefix(filespec, "https://") {
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

	// Handle as file path
	cleanPath := filepath.Clean(filespec)

	// Use data directory as base for file operations
	dataDir, err := getDataDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to get data directory: %w", err)
	}

	// Ensure data directory exists
	if dirErr := EnsureDataDirectory(); dirErr != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", dirErr)
	}

	// Reject absolute paths upfront
	if filepath.IsAbs(cleanPath) {
		return nil, fmt.Errorf("absolute paths not allowed: %s", filespec)
	}

	// Build and resolve paths for containment verification
	requestedPath := filepath.Join(dataDir, cleanPath)
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve data directory: %w", err)
	}

	absRequestedPath, err := filepath.Abs(requestedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve requested path: %w", err)
	}

	// Use filepath.Rel to verify the requested path is within data directory
	rel, err := filepath.Rel(absDataDir, absRequestedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to determine path relationship: %w", err)
	}

	// If rel starts with ".." or is "..", the path is outside data directory
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return nil, fmt.Errorf("path outside data directory: %s", filespec)
	}

	filePath := absRequestedPath

	file, err := os.Open(filePath)
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

// getFileNameFromSpec extracts the filename from a filespec (URL or file path)
func getFileNameFromSpec(filespec string) string {
	if filespec == "" {
		return ""
	}

	// For URLs, extract filename from the path
	if strings.HasPrefix(filespec, "http://") || strings.HasPrefix(filespec, "https://") {
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

	// For file paths, extract the base name
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

// uploadFiles uploads multiple files and returns their file IDs
func uploadFiles(ctx context.Context, client *model.Client4, channelID string, filespecs []string) ([]string, error) {
	var fileIDs []string

	for _, filespec := range filespecs {
		if filespec == "" {
			continue
		}

		fileData, err := fetchFileData(filespec)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch file %s: %w", filespec, err)
		}

		fileName := getFileNameFromSpec(filespec)
		if fileName == "" {
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

// handleFileAttachments handles file attachments upload and returns file IDs and a message
func handleFileAttachments(ctx context.Context, client *model.Client4, channelID string, attachments []string) ([]string, string) {
	var fileIDs []string
	var attachmentMessage string

	if len(attachments) > 0 {
		uploadedFileIDs, uploadErr := uploadFiles(ctx, client, channelID, attachments)
		if uploadErr != nil {
			attachmentMessage = fmt.Sprintf(" (file upload failed: %v)", uploadErr)
		} else {
			fileIDs = uploadedFileIDs
			attachmentMessage = fmt.Sprintf(" (uploaded %d files)", len(fileIDs))
		}
	}

	return fileIDs, attachmentMessage
}
