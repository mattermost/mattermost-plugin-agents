// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

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

// getFileNameFromSpec extracts the filename from a filespec (URL or file path)
func getFileNameFromSpec(filespec string) string {
	if filespec == "" {
		return ""
	}

	// For URLs, extract filename from the path
	if strings.HasPrefix(filespec, "http://") || strings.HasPrefix(filespec, "https://") {
		parts := strings.Split(filespec, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
		return "unknown"
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
