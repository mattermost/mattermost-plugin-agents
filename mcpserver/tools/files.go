// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
const readFileDescription = "Read the contents of a Mattermost file attachment by its File ID. Attachments in a conversation are shown to you as metadata (name, type, size, File ID) instead of inline content — call this tool with that File ID to read them. Images (JPEG/PNG/GIF/WebP) are returned as viewable image content; documents (PDF, Office) return server-extracted text; plain-text files return their raw text. Supports ranged reads via offset and limit to page through large text files. Parameters: file_id (required), offset (optional), limit (optional). Example: {\"file_id\": \"8xqzn3pfmtbyfkr9hqbw4hheoa\", \"offset\": 0, \"limit\": 6000}"

// getFileTools returns the file-related tools.
func (p *MattermostToolProvider) getFileTools() []MCPTool {
	return []MCPTool{
		{
			Name:         "read_file",
			Description:  readFileDescription,
			Schema:       NewJSONSchemaForAccessMode[ReadFileArgs](string(p.accessMode)),
			RichResolver: typedRich("read_file", p.toolReadFile),
		},
		{
			Name:        "get_file_info",
			Description: getFileInfoDescription,
			Schema:      NewJSONSchemaForAccessMode[GetFileInfoArgs](string(p.accessMode)),
			Resolver:    typed("get_file_info", p.toolGetFileInfo),
		},
		{
			Name:        "get_post_files",
			Description: getPostFilesDescription,
			Schema:      NewJSONSchemaForAccessMode[GetPostFilesArgs](string(p.accessMode)),
			Resolver:    typed("get_post_files", p.toolGetPostFiles),
		},
		{
			Name:        "get_file_link",
			Description: getFileLinkDescription,
			Schema:      NewJSONSchemaForAccessMode[GetFileLinkArgs](string(p.accessMode)),
			Resolver:    typed("get_file_link", p.toolGetFileLink),
		},
		{
			Name:        "search_files",
			Description: searchFilesDescription,
			Schema:      NewJSONSchemaForAccessMode[SearchFilesArgs](string(p.accessMode)),
			Resolver:    typed("search_files", p.toolSearchFiles),
		},
		{
			Name:        "upload_file",
			Description: uploadFileDescription,
			Schema:      NewJSONSchemaForAccessMode[UploadFileArgs](string(p.accessMode)),
			Resolver:    typed("upload_file", p.toolUploadFile),
		},
	}
}

// maxInlineImageBytes caps images returned as inline MCP image content. The
// limit mirrors the common LLM provider per-image request cap (5 MB).
const maxInlineImageBytes = 5 * 1024 * 1024

// isInlineImageMimeType reports whether mimeType is an image format that LLM
// providers accept as inline image content.
func isInlineImageMimeType(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

// isTextLikeMimeType reports whether mimeType can be served as plain text
// straight from the raw file bytes, without server-side extraction.
func isTextLikeMimeType(mimeType string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	if strings.HasPrefix(base, "text/") {
		return true
	}
	switch base {
	case "application/json", "application/xml", "application/javascript",
		"application/yaml", "application/x-yaml", "application/toml",
		"application/x-sh", "application/sql", "application/csv":
		return true
	}
	return false
}

// maxStandaloneTextBytes bounds how much of a raw text file the standalone
// fallback downloads into memory, mirroring files.Service's maxDownloadBytes.
const maxStandaloneTextBytes = 10 * 1024 * 1024

// readFileContentStandalone reads a file's text directly through the user's
// Mattermost client, for servers that cannot reach the plugin's extraction
// endpoint. Server-side document extraction (PDF, Office) is unavailable on
// this path, so only plain-text files have readable content.
func readFileContentStandalone(mcpContext *MCPToolContext, info *model.FileInfo, offset, limit int) (files.Content, error) {
	if !isTextLikeMimeType(info.MimeType) {
		return files.Content{Name: info.Name, MimeType: info.MimeType, HasText: false}, nil
	}

	if info.Size > maxStandaloneTextBytes {
		return files.Content{}, fmt.Errorf("file %q is %d bytes, larger than the %d-byte limit for direct text reads", info.Name, info.Size, maxStandaloneTextBytes)
	}

	data, _, err := mcpContext.Client.GetFile(mcpContext.Ctx, info.Id)
	if err != nil {
		return files.Content{}, fmt.Errorf("error downloading file: %w", err)
	}

	return files.Slice(info.Name, info.MimeType, string(data), offset, limit), nil
}

// toolReadFile implements the read_file tool.
func (p *MattermostToolProvider) toolReadFile(mcpContext *MCPToolContext, args ReadFileArgs) ([]mcp.Content, error) {
	if err := requireID("file_id", args.FileID); err != nil {
		return nil, err
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	info, _, err := client.GetFileInfo(ctx, args.FileID)
	if err != nil {
		return nil, fmt.Errorf("error fetching file info for %s: %w", args.FileID, err)
	}

	// Images are returned as inline image content, fetched directly with the
	// user's client (the extraction service is text-only).
	if isInlineImageMimeType(info.MimeType) {
		if info.Size > maxInlineImageBytes {
			return []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				"Image %q (%s) is %d bytes, larger than the %d-byte inline limit. Use get_file_link to get a URL instead.",
				info.Name, info.MimeType, info.Size, maxInlineImageBytes)}}, nil
		}
		data, _, err := client.GetFile(ctx, args.FileID)
		if err != nil {
			return nil, fmt.Errorf("error downloading image %s: %w", args.FileID, err)
		}
		return []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Image: %s (%s, %dx%d, %d bytes)", info.Name, info.MimeType, info.Width, info.Height, info.Size)},
			&mcp.ImageContent{Data: data, MIMEType: info.MimeType},
		}, nil
	}

	// Prefer the plugin's extraction service (the only source of text for PDF
	// and Office documents). When it fails for a plain-text file — e.g. a
	// standalone server pointed at a Mattermost without the plugin — fall back
	// to reading the raw bytes directly. Extraction failures for other types
	// are surfaced as errors: the fallback cannot read them, and reporting
	// "no extractable text" would disguise an outage as an empty file.
	if p.fileContentService != nil {
		content, svcErr := p.fileContentService.GetContent(ctx, mcpContext.UserID, args.FileID, args.Offset, args.Limit)
		if svcErr == nil {
			return []mcp.Content{&mcp.TextContent{Text: formatFileContent(content)}}, nil
		}
		if errors.Is(svcErr, files.ErrForbidden) {
			return nil, fmt.Errorf("you do not have permission to read this file")
		}
		if !isTextLikeMimeType(info.MimeType) {
			return nil, fmt.Errorf("error reading file %s: %w", args.FileID, svcErr)
		}
		p.logger.Debug("file content service unavailable, falling back to direct read", "file_id", args.FileID, "error", svcErr)
	}

	content, err := readFileContentStandalone(mcpContext, info, args.Offset, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", args.FileID, err)
	}
	return []mcp.Content{&mcp.TextContent{Text: formatFileContent(content)}}, nil
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

const (
	getFileInfoDescription  = "Get metadata for a single file/attachment. Parameters: file_id (required). Returns name, type, size, and File ID."
	getPostFilesDescription = "Get the file attachments on a post (metadata per file). Parameters: post_id (required)."
	getFileLinkDescription  = "Get a public permalink URL for a file. Parameters: file_id (required). Requires public links to be enabled on the server."
	searchFilesDescription  = "Search file attachments by filename/content within a team. Parameters: terms (required), team_id (required)."
	uploadFileDescription   = "Upload a file from a local path or URL to a channel and return its File ID. Local access only. Parameters: channel_id (required), path (required)."
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
	result.WriteString(fmt.Sprintf("Found %d file(s) on post %s:\n\n", len(infos), args.PostID))
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
	result.WriteString(fmt.Sprintf("Found %d file(s) for %q:\n\n", len(results.Order), args.Terms))
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
