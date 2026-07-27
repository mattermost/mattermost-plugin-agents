// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// CreateFileToolName is the runtime name of the built-in file creation tool.
	CreateFileToolName = "CreateFile"
	// maxCreatedFilesPerTurn caps CreateFile calls per response (Mattermost allows ~10 attachments per post).
	maxCreatedFilesPerTurn = 10
	// maxCreateFileContentBytes caps a single created file's content (1 MiB).
	maxCreateFileContentBytes = 1024 * 1024
	// maxCreateFileNameLength is the longest accepted file name.
	maxCreateFileNameLength = 255

	createFileDescription = "Create a text file that is attached to your reply. " +
		"Use this when the user asks for content as a downloadable file, document, or artifact (markdown, code, CSV, HTML, SVG, etc.), or when the output is too long to paste into the chat. " +
		"Text content only — binary formats are not supported. The file name's extension determines the file type. " +
		"The created file is attached to your reply automatically. Call this tool once per file, up to 10 files per reply. " +
		"Do NOT repeat the file's content in your response text — briefly mention the file instead."

	// createFileResultNote reminds the model not to duplicate attached content.
	createFileResultNote = "Attached to your reply automatically — do not repeat the file's content in your response text."
)

// CreateFileArgs is the LLM-visible input schema for the CreateFile tool.
type CreateFileArgs struct {
	FileName string `json:"file_name" jsonschema_description:"File name including extension; the extension determines the file type, e.g. report.md, data.csv, script.py"`
	Content  string `json:"content" jsonschema_description:"The complete text content of the file"`
}

// CreateFileResult is the tool result content. It is JSON so both the LLM
// and the response-attachment flow (which re-parses persisted tool results)
// can consume it.
type CreateFileResult struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	Note     string `json:"note"`
}

// NewCreateFileTool returns the built-in CreateFile tool. It uploads text
// content to the conversation channel so the response flow can attach it to
// the bot's reply post. AutoExecute is set because the tool's only side
// effect is scoped to the assistant's own response.
func NewCreateFileTool(client mmapi.Client) llm.Tool {
	return llm.Tool{
		Name:        CreateFileToolName,
		Description: createFileDescription,
		Schema:      llm.NewJSONSchemaFromStruct[CreateFileArgs](),
		AutoExecute: true,
		Resolver: func(_ context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			return resolveCreateFile(client, llmCtx, argsGetter)
		},
	}
}

func resolveCreateFile(client mmapi.Client, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreateFileArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for CreateFile tool: %w", err)
	}

	if client == nil {
		return "file creation is not available", errors.New("mattermost client unavailable for CreateFile")
	}
	if llmCtx == nil || llmCtx.Channel == nil || llmCtx.Channel.Id == "" {
		return "file creation is not available in this context because there is no conversation channel to hold the file", errors.New("CreateFile requires a channel-scoped context")
	}

	name := filepath.Base(strings.TrimSpace(args.FileName))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "file_name must be a plain file name with an optional extension, e.g. report.md or data.csv", errors.New("invalid CreateFile file name")
	}
	if utf8.RuneCountInString(name) > maxCreateFileNameLength {
		return fmt.Sprintf("file_name must be at most %d characters", maxCreateFileNameLength), errors.New("CreateFile file name too long")
	}

	if args.Content == "" {
		return "content must not be empty", errors.New("CreateFile content empty")
	}
	if len(args.Content) > maxCreateFileContentBytes {
		return "content exceeds the 1 MiB limit for a single file; split the content into multiple smaller files", errors.New("CreateFile content too large")
	}

	if len(llmCtx.CreatedFilesList()) >= maxCreatedFilesPerTurn {
		return fmt.Sprintf("the limit of %d created files per reply has been reached; do not create more files in this reply", maxCreatedFilesPerTurn), errors.New("CreateFile per-reply cap reached")
	}

	// UploadFile goes through the admin-level plugin API, which skips the
	// per-user checks the api4 upload endpoint performs. Enforce the server
	// attachment policy and the requesting user's upload permission in the
	// conversation channel explicitly (same pattern as files/files.go for
	// reads). A nil EnableFileAttachments means the Mattermost default (true).
	// Unattended/bot flows have no acting user; the channel is the bot's own
	// reply target, so a nil RequestingUser proceeds.
	if cfg := client.GetConfig(); cfg != nil && cfg.FileSettings.EnableFileAttachments != nil && !*cfg.FileSettings.EnableFileAttachments {
		return "file attachments are disabled on this server", errors.New("CreateFile rejected: file attachments are disabled by server config")
	}
	if llmCtx.RequestingUser != nil && !client.HasPermissionToChannel(llmCtx.RequestingUser.Id, llmCtx.Channel.Id, model.PermissionUploadFile) {
		return "you do not have permission to attach files in this channel", errors.New("CreateFile rejected: requesting user lacks upload permission in the channel")
	}

	info, err := client.UploadFile(strings.NewReader(args.Content), name, llmCtx.Channel.Id)
	if err != nil {
		return "file upload failed", fmt.Errorf("CreateFile upload failed: %w", err)
	}

	llmCtx.AddCreatedFile(llm.CreatedFile{ID: info.Id, Name: info.Name})

	result, err := json.Marshal(CreateFileResult{
		FileID:   info.Id,
		FileName: info.Name,
		Note:     createFileResultNote,
	})
	if err != nil {
		return "file upload failed", fmt.Errorf("failed to marshal CreateFile result: %w", err)
	}
	return string(result), nil
}

// ConsumeCreatedFiles returns the files created during this request without
// removing them (mirrors ConsumeWebSearchContexts).
func ConsumeCreatedFiles(ctx *llm.Context) []llm.CreatedFile {
	return ctx.CreatedFilesList()
}

// ParseCreateFileResult parses a persisted CreateFile tool result. ok is
// false when content is not a valid CreateFileResult with a file ID.
func ParseCreateFileResult(content string) (CreateFileResult, bool) {
	var result CreateFileResult
	if err := json.Unmarshal([]byte(content), &result); err != nil || !model.IsValidId(result.FileID) {
		return CreateFileResult{}, false
	}
	return result, true
}
