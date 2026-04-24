// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/httpservice"
)

// staticMCPURLFetchConfig provides defaults that mirror a locked-down Mattermost server
// (no TLS verification bypass, no allowlist of internal hostnames) for the standalone
// MCP binary's untrusted URL fetches.
var staticMCPURLFetchConfig = &model.Config{
	ServiceSettings: model.ServiceSettings{
		EnableInsecureOutgoingConnections:   model.NewPointer(false),
		AllowedUntrustedInternalConnections: model.NewPointer(""),
	},
}

// staticMCPConfigService implements the getConfig type expected by httpservice.MakeHTTPService.
type staticMCPConfigService struct{}

func (staticMCPConfigService) Config() *model.Config {
	return staticMCPURLFetchConfig
}

// maxMCPFetchBytes matches the default model.FileSettings.MaxFileSize (100 MiB) for local attachment reads.
const maxMCPFetchBytes = 100 * 1024 * 1024

var mcpLocalURLHTTPClientInstance *http.Client
var mcpLocalURLHTTPClientOnce sync.Once

func getMCPLocalURLHTTPClient() *http.Client {
	mcpLocalURLHTTPClientOnce.Do(func() {
		mcpLocalURLHTTPClientInstance = httpservice.MakeHTTPService(staticMCPConfigService{}).MakeClient(false)
	})
	return mcpLocalURLHTTPClientInstance
}

// errMCPFileUploadFailed is returned to tool output when an attachment URL fetch fails.
// The underlying error is logged; do not wrap with %w from low-level clients to avoid leaking details to users.
var errMCPFileUploadFailed = errors.New("file upload failed")

func mcpLogAttachmentURLFailureAndReturn(err error) error {
	if err == nil {
		return nil
	}
	log.Printf("mattermost-mcp: attachment URL fetch: %v", err)
	return errMCPFileUploadFailed
}

// readLimitedToMaxMCPBytes reads r with the same size cap for URL and data-directory file sources.
func readLimitedToMaxMCPBytes(r io.Reader) ([]byte, error) {
	limited := io.LimitReader(r, maxMCPFetchBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxMCPFetchBytes {
		return nil, fmt.Errorf("content too large (max %d bytes)", maxMCPFetchBytes)
	}
	return data, nil
}

// GetDataDirectoryInternal is the internal function that can be overridden in tests
var GetDataDirectoryInternal = getDataDirectory

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
	if filepath.Base(execDir) != "bin" {
		return "", fmt.Errorf("executable not in expected 'bin' directory structure")
	}

	return dataDir, nil
}

// EnsureDataDirectory creates the data directory if it doesn't exist
func EnsureDataDirectory() error {
	dataDir, err := GetDataDirectoryInternal()
	if err != nil {
		return err
	}
	return os.MkdirAll(dataDir, 0700)
}

// fetchFileDataForLocal fetches file data from a file path or URL (local access only)
func fetchFileDataForLocal(ctx context.Context, filespec string, accessMode AccessMode) ([]byte, error) {
	if filespec == "" {
		return nil, fmt.Errorf("empty filespec provided")
	}

	// URLs are only allowed for local access mode
	if accessMode != AccessModeLocal {
		return nil, fmt.Errorf("URL access not supported in remote access mode, only local access allows URL access")
	}

	// Check if it's a URL
	if isURL(filespec) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parsed, err := url.Parse(filespec)
		if err != nil {
			return nil, mcpLogAttachmentURLFailureAndReturn(fmt.Errorf("parse URL: %w", err))
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, mcpLogAttachmentURLFailureAndReturn(fmt.Errorf("unsupported URL scheme: %q", parsed.Scheme))
		}
		if parsed.Host == "" {
			return nil, mcpLogAttachmentURLFailureAndReturn(fmt.Errorf("URL missing host"))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return nil, mcpLogAttachmentURLFailureAndReturn(err)
		}
		resp, err := getMCPLocalURLHTTPClient().Do(req)
		if err != nil {
			return nil, mcpLogAttachmentURLFailureAndReturn(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, mcpLogAttachmentURLFailureAndReturn(fmt.Errorf("HTTP %d", resp.StatusCode))
		}

		data, err := readLimitedToMaxMCPBytes(resp.Body)
		if err != nil {
			return nil, mcpLogAttachmentURLFailureAndReturn(err)
		}
		return data, nil
	}

	cleanPath := filepath.Clean(filespec)

	// Use data directory as base for file operations
	dataDir, err := GetDataDirectoryInternal()
	if err != nil {
		return nil, fmt.Errorf("failed to get data directory: %w", err)
	}

	// Ensure data directory exists
	if dirErr := EnsureDataDirectory(); dirErr != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", dirErr)
	}

	// Use os.Root for secure path traversal protection
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open data directory root: %w", err)
	}
	defer root.Close()

	file, err := root.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	data, err := readLimitedToMaxMCPBytes(file)
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

		fileData, err := fetchFileDataForLocal(ctx, filespec, accessMode)
		if err != nil {
			if errors.Is(err, errMCPFileUploadFailed) {
				return nil, err
			}
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
			if errors.Is(uploadErr, errMCPFileUploadFailed) {
				attachmentMessage = " (file upload failed)"
			} else {
				attachmentMessage = fmt.Sprintf(" (file upload failed: %v)", uploadErr)
			}
		} else {
			fileIDs = uploadedFileIDs
			attachmentMessage = fmt.Sprintf(" (uploaded %d files)", len(fileIDs))
		}
	}

	return fileIDs, attachmentMessage
}
