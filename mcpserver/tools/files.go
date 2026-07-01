// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
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
		{
			Name:        "read_file",
			Description: readFileDescription,
			Schema:      NewJSONSchemaForAccessMode[ReadFileArgs](string(p.accessMode)),
			Resolver:    typed("read_file", p.toolReadFile),
		},
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
