// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// FileContentService reads the text contents of Mattermost file attachments on
// behalf of a user, enforcing that user's channel permissions. *files.Service
// implements it directly for embedded servers; HTTPFileContentService calls back
// to the plugin endpoint for external servers.
type FileContentService interface {
	GetContent(ctx context.Context, userID, fileID string, offset, limit int) (files.Content, error)
}

// ReadFileArgs represents arguments for the read_file tool.
type ReadFileArgs struct {
	FileID string `json:"file_id" jsonschema:"The ID of the file to read; use the File ID shown in the attached-file metadata,minLength=26,maxLength=26"`
	Offset int    `json:"offset,omitempty" jsonschema:"Character offset to start reading from (default 0). Pass the next offset reported by a previous call to page through a large file."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of characters to return (default 6000, capped at 20000)."`
}

// readFileDescription is the MCP tool metadata description for read_file.
const readFileDescription = "Read the text contents of a Mattermost file attachment by its File ID. Large attachments in a conversation are shown to you as metadata (name, type, size, File ID) instead of inline content — call this tool with that File ID to read them. Returns extracted text for documents (PDF, Office) and the raw text for plain-text files. Supports ranged reads via offset and limit to page through large files. Parameters: file_id (required), offset (optional), limit (optional). Example: {\"file_id\": \"8xqzn3pfmtbyfkr9hqbw4hheoa\", \"offset\": 0, \"limit\": 6000}"

// getFileTools returns the file-related tools.
func (p *MattermostToolProvider) getFileTools() []MCPTool {
	return []MCPTool{
		mcpReadTool(p, "read_file", readFileDescription, p.toolReadFile),
		mcpReadTool(p, "get_file_info", getFileInfoDescription, p.toolGetFileInfo),
		mcpReadTool(p, "get_post_files", getPostFilesDescription, p.toolGetPostFiles),
		mcpReadTool(p, "get_file_link", getFileLinkDescription, p.toolGetFileLink),
		mcpReadTool(p, "search_files", searchFilesDescription, p.toolSearchFiles),
		mcpTool(p, "upload_file", uploadFileDescription, p.toolUploadFile),
		mcpTool(p, "copy_file_to_channel", copyFileToChannelDescription, p.toolCopyFileToChannel),
	}
}

// toolReadFile implements the read_file tool.
func (p *MattermostToolProvider) toolReadFile(mcpContext *MCPToolContext, args ReadFileArgs) (string, error) {
	if err := requireID("file_id", args.FileID); err != nil {
		return "", err
	}

	if p.fileContentService == nil {
		return "file reading is not available", nil
	}

	content, err := p.fileContentService.GetContent(mcpContext.Ctx, mcpContext.UserID, args.FileID, args.Offset, args.Limit)
	if err != nil {
		if errors.Is(err, files.ErrForbidden) {
			return "you do not have permission to read this file", nil
		}
		return "", fmt.Errorf("error reading file %s: %w", args.FileID, err)
	}

	return formatFileContent(content), nil
}

// formatFileContent renders a ranged file read for the LLM, including paging
// instructions when more content remains.
func formatFileContent(c files.Content) string {
	if !c.HasText {
		return fmt.Sprintf("File %q (%s) has no extractable text content and cannot be read as text.", c.Name, c.MimeType)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "File: %s", c.Name)
	if c.MimeType != "" {
		fmt.Fprintf(&b, " (%s)", c.MimeType)
	}
	b.WriteString("\n")

	end := c.Offset + c.Returned
	fmt.Fprintf(&b, "Showing characters %d-%d of %d.", c.Offset, end, c.TotalRunes)
	if c.HasMore {
		fmt.Fprintf(&b, " More content remains; call read_file again with offset=%d to continue.", end)
	}
	b.WriteString("\n\n")
	b.WriteString(c.Text)

	return b.String()
}

// --- Additional file tools (info, post files, link, search, upload) ---

// GetFileInfoArgs represents arguments for the get_file_info tool.
type GetFileInfoArgs struct {
	FileID string `json:"file_id" jsonschema:"The file/attachment ID,minLength=26,maxLength=26"`
}

// GetPostFilesArgs represents arguments for the get_post_files tool.
type GetPostFilesArgs struct {
	PostID string `json:"post_id" jsonschema:"The post whose file attachments to list,minLength=26,maxLength=26"`
}

// GetFileLinkArgs represents arguments for the get_file_link tool.
type GetFileLinkArgs struct {
	FileID string `json:"file_id" jsonschema:"The file to get a public permalink for,minLength=26,maxLength=26"`
}

// SearchFilesArgs represents arguments for the search_files tool.
type SearchFilesArgs struct {
	Terms  string `json:"terms" jsonschema:"Search terms to match against file names/content,minLength=1,maxLength=4000"`
	TeamID string `json:"team_id" jsonschema:"The team to search within,minLength=26,maxLength=26"`
}

// UploadFileArgs represents arguments for the upload_file tool.
type UploadFileArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel to upload the file to,minLength=26,maxLength=26"`
	Path      string `json:"path" access:"local" jsonschema:"Local file path or URL to upload"`
}

// CopyFileToChannelArgs represents arguments for the copy_file_to_channel tool.
type CopyFileToChannelArgs struct {
	FileID          string `json:"file_id" jsonschema:"The ID of the existing Mattermost file to copy,minLength=26,maxLength=26"`
	TargetChannelID string `json:"target_channel_id" jsonschema:"The destination channel ID (or board ID, since a board is a channel) to upload the copy to,minLength=26,maxLength=26"`
	Filename        string `json:"filename,omitempty" jsonschema:"Optional plain filename with extension to use for the copy (no path separators). Defaults to the source file's name.,maxLength=255"`
}

const (
	getFileInfoDescription       = "Get metadata for a single file/attachment. Parameters: file_id (required). Returns name, type, size, and File ID."
	getPostFilesDescription      = "Get the file attachments on a post (metadata per file). Parameters: post_id (required)."
	getFileLinkDescription       = "Get a public permalink URL for a file. Parameters: file_id (required). Requires public links to be enabled on the server."
	searchFilesDescription       = "Search file attachments by filename/content within a team. Parameters: terms (required), team_id (required)."
	uploadFileDescription        = "Upload a file from a local path or URL to a channel and return its File ID. Local access only. Parameters: channel_id (required), path (required)."
	copyFileToChannelDescription = "Copy an existing Mattermost file (such as an image or document uploaded in a DM or another channel) into a target channel and return the new File ID. Use this before attaching a file from another channel or post to a Boards card (a board's board_id is its channel_id). Enforces your read access to the source file and upload permission in the target channel. Parameters: file_id (required), target_channel_id (required), filename (optional)."
)

// toolGetFileInfo implements the get_file_info tool.
func (p *MattermostToolProvider) toolGetFileInfo(mcpContext *MCPToolContext, args GetFileInfoArgs) (string, error) {
	if err := requireID("file_id", args.FileID); err != nil {
		return "", err
	}
	info, _, err := mcpContext.Client.GetFileInfo(mcpContext.Ctx, args.FileID)
	if err != nil {
		return "", fmt.Errorf("error fetching file info: %w", err)
	}
	var result strings.Builder
	format.WriteFileDescriptor(&result, format.FileDescriptorEntry{FileInfo: info})
	return result.String(), nil
}

// toolGetPostFiles implements the get_post_files tool.
func (p *MattermostToolProvider) toolGetPostFiles(mcpContext *MCPToolContext, args GetPostFilesArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}
	infos, _, err := mcpContext.Client.GetFileInfosForPost(mcpContext.Ctx, args.PostID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching post files: %w", err)
	}
	if len(infos) == 0 {
		return "no files attached to this post", nil
	}
	var result strings.Builder
	fmt.Fprintf(&result, "Found %d file(s) on post %s:\n\n", len(infos), args.PostID)
	for i, info := range infos {
		format.WriteFileDescriptor(&result, format.FileDescriptorEntry{Number: i + 1, FileInfo: info})
	}
	return result.String(), nil
}

// toolGetFileLink implements the get_file_link tool.
func (p *MattermostToolProvider) toolGetFileLink(mcpContext *MCPToolContext, args GetFileLinkArgs) (string, error) {
	if err := requireID("file_id", args.FileID); err != nil {
		return "", err
	}
	link, _, err := mcpContext.Client.GetFileLink(mcpContext.Ctx, args.FileID)
	if err != nil {
		return "", fmt.Errorf("error fetching file link (public links may be disabled on the server): %w", err)
	}
	return fmt.Sprintf("Public link for file %s:\n%s", args.FileID, link), nil
}

// toolSearchFiles implements the search_files tool.
func (p *MattermostToolProvider) toolSearchFiles(mcpContext *MCPToolContext, args SearchFilesArgs) (string, error) {
	if args.Terms == "" {
		return "", fmt.Errorf("terms cannot be empty")
	}
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}

	results, _, err := mcpContext.Client.SearchFiles(mcpContext.Ctx, args.TeamID, args.Terms, false)
	if err != nil {
		return "", fmt.Errorf("error searching files: %w", err)
	}
	if results == nil || len(results.Order) == 0 {
		return fmt.Sprintf("no files found for %q", args.Terms), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d file(s) for %q:\n\n", len(results.Order), args.Terms)
	for i, id := range results.Order {
		info := results.FileInfos[id]
		if info == nil {
			continue
		}
		format.WriteFileDescriptor(&result, format.FileDescriptorEntry{Number: i + 1, FileInfo: info})
	}
	return result.String(), nil
}

// toolUploadFile implements the upload_file tool. File uploads from a path/URL are
// only supported in local access mode.
func (p *MattermostToolProvider) toolUploadFile(mcpContext *MCPToolContext, args UploadFileArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if mcpContext.AccessMode != AccessModeLocal {
		return "file uploads from a path or URL are only supported in local access mode", nil
	}
	if args.Path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	fileIDs, err := uploadFilesForLocal(mcpContext.Ctx, mcpContext.Client, args.ChannelID, []string{args.Path}, mcpContext.AccessMode)
	if err != nil {
		return "", fmt.Errorf("error uploading file: %w", err)
	}
	if len(fileIDs) == 0 {
		return "", fmt.Errorf("upload produced no file")
	}
	return fmt.Sprintf("Successfully uploaded file to channel %s. File ID: %s", args.ChannelID, fileIDs[0]), nil
}

// toolCopyFileToChannel copies an existing Mattermost file into target_channel_id
// using the caller's authenticated Mattermost client, so both source file-download
// policy/channel read permissions and target channel upload permissions apply.
func (p *MattermostToolProvider) toolCopyFileToChannel(mcpContext *MCPToolContext, args CopyFileToChannelArgs) (string, error) {
	if err := requireID("file_id", args.FileID); err != nil {
		return "", err
	}
	if err := requireID("target_channel_id", args.TargetChannelID); err != nil {
		return "", err
	}

	var overrideName string
	if args.Filename != "" {
		var err error
		overrideName, err = validatePlainCopyFilename(args.Filename)
		if err != nil {
			return "", err
		}
	}

	ctx := mcpContext.Ctx
	client := mcpContext.Client

	info, _, err := client.GetFileInfo(ctx, args.FileID)
	if err != nil {
		return "", fmt.Errorf("error fetching source file info: %w", err)
	}
	if info == nil || info.DeleteAt != 0 {
		return "", fmt.Errorf("source file %s was not found", args.FileID)
	}
	if info.Size > maxMCPFetchBytes {
		return "", fmt.Errorf("source file %q is too large (%d bytes; max %d bytes)", info.Name, info.Size, maxMCPFetchBytes)
	}

	fileName := overrideName
	if fileName == "" {
		fileName = fallbackCopyFilename(info.Name)
	}

	data, _, err := client.GetFile(ctx, args.FileID)
	if err != nil {
		return "", fmt.Errorf("error downloading source file: %w", err)
	}
	if int64(len(data)) > maxMCPFetchBytes {
		return "", fmt.Errorf("source file %q is too large (%d bytes; max %d bytes)", fileName, len(data), maxMCPFetchBytes)
	}

	var uploadResp *model.FileUploadResponse
	if len(data) == 0 {
		// Mattermost's raw-body upload rejects Content-Length: 0; multipart handles empty files.
		uploadResp, _, err = client.UploadFile(ctx, data, args.TargetChannelID, fileName)
	} else {
		uploadResp, _, err = client.UploadFileAsRequestBody(ctx, data, args.TargetChannelID, fileName)
	}
	if err != nil {
		return "", fmt.Errorf("error uploading copied file to channel %s: %w", args.TargetChannelID, err)
	}
	if uploadResp == nil || len(uploadResp.FileInfos) == 0 || uploadResp.FileInfos[0] == nil {
		return "", fmt.Errorf("upload produced no file")
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Copied file to channel %s:\n", args.TargetChannelID)
	format.WriteFileDescriptor(&result, format.FileDescriptorEntry{FileInfo: uploadResp.FileInfos[0]})
	return result.String(), nil
}

func validatePlainCopyFilename(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, `/\`) {
		return "", fmt.Errorf("invalid filename %q: provide a plain filename with an extension such as 'screenshot.png'", raw)
	}
	name := filepath.Base(trimmed)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid filename %q: provide a plain filename with an extension such as 'screenshot.png'", raw)
	}
	if utf8.RuneCountInString(name) > 255 {
		return "", fmt.Errorf("filename %q is too long: filenames must be at most 255 characters", name)
	}
	return name, nil
}

func fallbackCopyFilename(sourceName string) string {
	trimmed := strings.TrimSpace(sourceName)
	if trimmed == "" {
		return "attachment"
	}
	name := filepath.Base(strings.ReplaceAll(trimmed, `\`, "/"))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "attachment"
	}
	if runes := []rune(name); len(runes) > 255 {
		return string(runes[:255])
	}
	return name
}
