// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	otelcodes "go.opentelemetry.io/otel/codes"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// AttachSandboxFileToolName is the runtime name of the built-in tool that
	// attaches provider-sandbox output files to the reply.
	AttachSandboxFileToolName = "AttachSandboxFile"

	attachSandboxFileDescription = "Attach a file you created in the code execution sandbox to your reply. " +
		"Files created in the sandbox are NOT visible to the user until attached with this tool. " +
		"Pass the file_id exactly as it appears in the code execution result, and a file_name whose extension matches the file's format. " +
		"Call this tool once per file, up to 10 files per reply, and only for files worth sharing — not intermediate or scratch files. " +
		"The file is attached to your reply automatically; do not repeat its content in your response text. " +
		"Only state that a file is attached after this tool has returned its file_id."
)

// AttachSandboxFileArgs is the LLM-visible input schema for the tool.
type AttachSandboxFileArgs struct {
	FileID   string `json:"file_id" jsonschema_description:"The file id from the code execution result, e.g. file_011CNha8iCJcU1wXNR6q4V8w"`
	FileName string `json:"file_name" jsonschema_description:"File name including extension matching the file's format, e.g. report.csv, chart.png"`
}

// NewAttachSandboxFileTool returns the built-in AttachSandboxFile tool. It
// downloads a sandbox-created file from the provider with the agent's
// credentials and uploads it to the conversation channel so the response flow
// attaches it to the bot's reply post (the same flow CreateFile uses).
// AutoExecute is set because the tool's only side effect is scoped to the
// assistant's own response, mirroring CreateFile.
func NewAttachSandboxFileTool(client mmapi.Client, downloader llm.ProviderFileDownloader) llm.Tool {
	return llm.Tool{
		Name:        AttachSandboxFileToolName,
		Description: attachSandboxFileDescription,
		Schema:      llm.NewJSONSchemaFromStruct[AttachSandboxFileArgs](),
		AutoExecute: true,
		Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			return resolveAttachSandboxFile(ctx, client, downloader, llmCtx, argsGetter)
		},
	}
}

func resolveAttachSandboxFile(
	ctx context.Context,
	client mmapi.Client,
	downloader llm.ProviderFileDownloader,
	llmCtx *llm.Context,
	argsGetter llm.ToolArgumentGetter,
) (string, error) {
	var args AttachSandboxFileArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for AttachSandboxFile tool: %w", err)
	}

	if client == nil || downloader == nil {
		return "sandbox file attachment is not available", errors.New("AttachSandboxFile requires a mattermost client and a provider file downloader")
	}
	if llmCtx == nil || llmCtx.Channel == nil || llmCtx.Channel.Id == "" {
		return "file attachment is not available in this context because there is no conversation channel to hold the file", errors.New("AttachSandboxFile requires a channel-scoped context")
	}
	if llmCtx.RequestingUser == nil || llmCtx.RequestingUser.Id == "" {
		return "file attachment is not available in this context", errors.New("AttachSandboxFile requires a requesting user")
	}

	// Only ids observed in this request's server-tool activity may be
	// downloaded. This blocks hallucinated ids and prompt-injected attempts to
	// pull arbitrary org files reachable with the provider API key.
	fileID := strings.TrimSpace(args.FileID)
	if fileID == "" || !llmCtx.IsSandboxFileID(fileID) {
		return "file_id was not produced by the code execution sandbox in this conversation turn; pass a file_id exactly as it appears in a code execution result", errors.New("AttachSandboxFile rejected: unobserved file id")
	}

	name := filepath.Base(strings.TrimSpace(args.FileName))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "file_name must be a plain file name with an optional extension, e.g. report.csv or chart.png", errors.New("invalid AttachSandboxFile file name")
	}
	if utf8.RuneCountInString(name) > maxCreateFileNameLength {
		return fmt.Sprintf("file_name must be at most %d characters", maxCreateFileNameLength), errors.New("AttachSandboxFile file name too long")
	}

	// Mirror CreateFile's policy enforcement: UploadFile bypasses the api4
	// per-user checks, so enforce attachment policy, size limit, per-reply
	// cap, and the requesting user's upload permission here.
	cfg := client.GetConfig()
	if cfg != nil && cfg.FileSettings.EnableFileAttachments != nil && !*cfg.FileSettings.EnableFileAttachments {
		return "file attachments are disabled on this server", errors.New("AttachSandboxFile rejected: file attachments are disabled by server config")
	}

	if slots := min(llmCtx.ResponseAttachmentSlots(), maxCreatedFilesPerTurn); len(llmCtx.CreatedFilesList()) >= slots {
		return fmt.Sprintf("no more files can be attached to this reply (limit %d per post); do not attach more files in this reply", maxCreatedFilesPerTurn), errors.New("AttachSandboxFile per-reply cap reached")
	}

	if !client.HasPermissionToChannel(llmCtx.RequestingUser.Id, llmCtx.Channel.Id, model.PermissionUploadFile) {
		return "you do not have permission to attach files in this channel", errors.New("AttachSandboxFile rejected: requesting user lacks upload permission in the channel")
	}

	downloadCtx, span := telemetry.Tracer().Start(ctx, "attach sandbox file download")
	content, _, err := downloader.DownloadProviderFile(downloadCtx, fileID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "download failed")
		span.End()
		return "downloading the file from the sandbox failed", fmt.Errorf("AttachSandboxFile download failed: %w", err)
	}
	span.End()

	if len(content) == 0 {
		return "the sandbox file is empty", errors.New("AttachSandboxFile rejected: empty file content")
	}
	if limit := createFileContentLimit(cfg); int64(len(content)) > limit {
		return fmt.Sprintf("the file exceeds the %d-byte file size limit and cannot be attached", limit), errors.New("AttachSandboxFile content too large")
	}

	_, uploadSpan := telemetry.Tracer().Start(ctx, "attach sandbox file upload")
	info, err := client.UploadFile(bytes.NewReader(content), name, llmCtx.Channel.Id)
	if err != nil {
		uploadSpan.RecordError(err)
		uploadSpan.SetStatus(otelcodes.Error, "upload failed")
		uploadSpan.End()
		return "file upload failed", fmt.Errorf("AttachSandboxFile upload failed: %w", err)
	}
	uploadSpan.End()

	llmCtx.AddCreatedFile(llm.CreatedFile{ID: info.Id, Name: info.Name})

	// Reuse the CreateFileResult shape so the response-attachment flow's
	// turn-scan fallback (ParseCreateFileResult) recognizes this result too.
	result, err := json.Marshal(CreateFileResult{
		FileID:   info.Id,
		FileName: info.Name,
		Note:     createFileResultNote,
	})
	if err != nil {
		return "file upload failed", fmt.Errorf("failed to marshal AttachSandboxFile result: %w", err)
	}
	return string(result), nil
}
